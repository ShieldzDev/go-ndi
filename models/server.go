package models

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ShieldzDev/go-ndi/utils"
	"github.com/ShieldzDev/go-ndi/web"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

const (
	tokenLifetime = 15 * time.Minute
	maxMessage    = 1024
)

type Server struct {
	hub      *Hub
	secret   []byte
	limitMu  sync.Mutex
	limiters map[string]*rate.Limiter
}

func NewServer(secret []byte) *Server {
	return &Server{hub: NewHub(), secret: secret, limiters: make(map[string]*rate.Limiter)}
}

func (s *Server) RunJanitor(ctx context.Context) { s.hub.runJanitor(ctx) }

type tokenClaims struct {
	Code string `json:"c"`
	ID   string `json:"i"`
	Name string `json:"n"`
	Exp  int64  `json:"e"`
}

type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(self)")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://unpkg.com; style-src 'self' 'unsafe-inline' https://unpkg.com https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https://tile.openstreetmap.org; connect-src 'self' ws: wss:; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func mustReadWeb(name string) []byte {
	data, err := web.Files.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}

func (s *Server) signToken(claims tokenClaims) string {
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) verifyToken(token string) (tokenClaims, error) {
	var claims tokenClaims
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return claims, errors.New("invalid token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return claims, errors.New("invalid token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.Code == "" || claims.ID == "" || claims.Name == "" || claims.Exp <= time.Now().Unix() {
		return tokenClaims{}, errors.New("invalid or expired token")
	}
	return claims, nil
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r, "create", 5, time.Hour) {
		utils.WriteError(w, http.StatusTooManyRequests, "rate_limited", "Too many room creation requests")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := utils.DecodeJSON(w, r, &body); err != nil || len(body.Password) > 72 {
		utils.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid request body or password (max 72 bytes)")
		return
	}
	var hash []byte
	if body.Password != "" {
		var err error
		hash, err = bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "internal", "Could not create room")
			return
		}
	}
	code, err := s.hub.create(hash)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "internal", "Could not create room")
		return
	}
	utils.WriteJSON(w, http.StatusCreated, map[string]string{"code": code})
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	code := utils.NormalizeCode(chi.URLParam(r, "code"))
	exists, full, needsPassword := s.hub.status(code)
	if !exists {
		utils.WriteError(w, http.StatusNotFound, "room_not_found", "Room not found")
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"code": code, "full": full, "needsPassword": needsPassword})
}

func (s *Server) joinRoom(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r, "join", 20, time.Minute) {
		utils.WriteError(w, http.StatusTooManyRequests, "rate_limited", "Too many join attempts")
		return
	}
	code := utils.NormalizeCode(chi.URLParam(r, "code"))
	if len(code) != 6 {
		utils.WriteError(w, http.StatusNotFound, "room_not_found", "Room not found")
		return
	}
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := utils.DecodeJSON(w, r, &body); err != nil || !utils.ValidName(body.Name) || len(body.Password) > 72 {
		utils.WriteError(w, http.StatusBadRequest, "invalid_request", "Enter a name of 1–32 characters and a password of at most 72 bytes")
		return
	}
	s.hub.mu.RLock()
	rm := s.hub.rooms[code]
	if rm == nil {
		s.hub.mu.RUnlock()
		utils.WriteError(w, http.StatusNotFound, "room_not_found", "Room not found")
		return
	}
	hash := append([]byte(nil), rm.passwordHash...)
	full := len(rm.members) >= maxMembers
	s.hub.mu.RUnlock()

	if full {
		utils.WriteError(w, http.StatusConflict, "room_full", "This room already has 10 members")
		return
	}

	if len(hash) > 0 && bcrypt.CompareHashAndPassword(hash, []byte(body.Password)) != nil {
		utils.WriteError(w, http.StatusUnauthorized, "wrong_password", "Incorrect room password")
		return
	}

	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "internal", "Could not create join token")
		return
	}

	claims := tokenClaims{Code: code, ID: hex.EncodeToString(idBytes[:]),
		Name: strings.TrimSpace(body.Name),
		Exp:  time.Now().Add(tokenLifetime).Unix()}

	utils.WriteJSON(w, http.StatusOK, map[string]any{"token": s.signToken(claims), "expiresIn": int(tokenLifetime.Seconds())})
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	claims, err := s.verifyToken(r.URL.Query().Get("token"))
	if err != nil {
		utils.WriteError(w, http.StatusUnauthorized, "bad_token", "Invalid or expired join token")
		return
	}
	if exists, _, _ := s.hub.status(claims.Code); !exists {
		utils.WriteError(w, http.StatusNotFound, "room_not_found", "Room not found")
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, parseErr := url.Parse(origin)
		if parseErr != nil || !strings.EqualFold(u.Host, r.Host) {
			utils.WriteError(w, http.StatusForbidden, "bad_origin", "WebSocket origin not allowed")
			return
		}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{r.Host}})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxMessage)
	c := &Client{id: claims.ID, name: claims.Name, conn: conn, send: make(chan []byte, 32)}
	if _, err := s.hub.register(claims.Code, c); err != nil {
		message, _ := json.Marshal(map[string]any{"type": "error", "data": map[string]string{"code": err.Error(), "message": "Could not join room"}})
		_ = conn.Write(r.Context(), websocket.MessageText, message)
		_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	writerDone := make(chan struct{})
	go s.writePump(ctx, c, writerDone)
	go func() {
		select {
		case <-writerDone:
			cancel()
			_ = conn.Close(websocket.StatusGoingAway, "writer stopped")
		case <-ctx.Done():
		}
	}()
	s.readPump(ctx, claims.Code, c)
	cancel()
	_ = conn.Close(websocket.StatusNormalClosure, "")
	<-writerDone
	s.hub.unregister(claims.Code, c.id)
}

func (s *Server) readPump(ctx context.Context, code string, c *Client) {
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg envelope
		if json.Unmarshal(data, &msg) != nil || msg.Type != "location_update" {
			continue
		}
		var loc location
		if json.Unmarshal(msg.Data, &loc) != nil ||
			math.IsNaN(loc.Lat) || math.IsNaN(loc.Lng) ||
			math.IsNaN(loc.Accuracy) || math.IsInf(loc.Lat, 0) ||
			math.IsInf(loc.Lng, 0) || math.IsInf(loc.Accuracy, 0) ||
			loc.Lat < -90 || loc.Lat > 90 || loc.Lng < -180 || loc.Lng > 180 ||
			loc.Accuracy < 0 || loc.Accuracy > 100000 {
			continue
		}
		now := time.Now()
		if !c.lastSent.IsZero() && now.Sub(c.lastSent) < time.Second {
			continue
		}
		c.lastSent = now
		s.hub.update(code, c, loc)
	}
}

func (s *Server) writePump(ctx context.Context, c *Client, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) allow(r *http.Request, action string, burst int, period time.Duration) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	key := action + ":" + ip
	s.limitMu.Lock()
	limiter := s.limiters[key]
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Limit(float64(burst)/period.Seconds()), burst)
		s.limiters[key] = limiter
	}
	s.limitMu.Unlock()
	return limiter.Allow()
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(securityHeaders)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(mustReadWeb("index.html"))
	})
	r.Get("/static/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := chi.URLParam(r, "file")
		if file != "app.js" && file != "style.css" {
			http.NotFound(w, r)
			return
		}
		contentType := "text/javascript; charset=utf-8"
		if file == "style.css" {
			contentType = "text/css; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(mustReadWeb(file))
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	r.Route("/api", func(r chi.Router) {
		r.Post("/rooms", s.createRoom)
		r.Get("/rooms/{code}", s.getRoom)
		r.Post("/rooms/{code}/join", s.joinRoom)
	})
	r.Get("/ws", s.websocket)
	return r
}
