package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRoomMemberCapUnderConcurrency(t *testing.T) {
	h := NewHub()
	h.rooms["ABC234"] = &Room{code: "ABC234", lastActivity: time.Now(), members: make(map[string]*Client)}

	var wg sync.WaitGroup
	var mu sync.Mutex
	joined := 0
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := &Client{id: fmt.Sprintf("member-%d", i), name: "test", send: make(chan []byte, 32)}
			_, err := h.register("ABC234", c)
			if err == nil {
				mu.Lock()
				joined++
				mu.Unlock()
			} else if err.Error() != "room_full" {
				t.Errorf("register failed: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if joined != maxMembers {
		t.Fatalf("joined %d members; want %d", joined, maxMembers)
	}
	if got := len(h.rooms["ABC234"].members); got != maxMembers {
		t.Fatalf("room has %d members; want %d", got, maxMembers)
	}
}

func TestJoinTokenSignatureAndExpiry(t *testing.T) {
	s := NewServer([]byte("0123456789abcdef0123456789abcdef"))
	claims := tokenClaims{Code: "ABC234", ID: "member", Name: "Friend", Exp: time.Now().Add(time.Minute).Unix()}
	token := s.signToken(claims)
	if got, err := s.verifyToken(token); err != nil || got.ID != claims.ID {
		t.Fatalf("valid token rejected: claims=%+v err=%v", got, err)
	}
	if _, err := s.verifyToken(token + "x"); err == nil {
		t.Fatal("tampered token was accepted")
	}
	claims.Exp = time.Now().Add(-time.Minute).Unix()
	if _, err := s.verifyToken(s.signToken(claims)); err == nil {
		t.Fatal("expired token was accepted")
	}
}

func TestHTTPFlowAndEmbeddedFrontend(t *testing.T) {
	s := NewServer([]byte("0123456789abcdef0123456789abcdef"))
	h := s.Router()

	for _, path := range []string{"/", "/static/app.js", "/static/style.css", "/healthz"} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK || res.Body.Len() == 0 {
			t.Fatalf("GET %s: status=%d body=%q", path, res.Code, res.Body.String())
		}
	}

	create := httptest.NewRecorder()
	h.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/rooms", bytes.NewBufferString(`{"password":"secret"}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create room: status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil || len(created.Code) != 6 {
		t.Fatalf("invalid create response: %s", create.Body.String())
	}

	wrong := httptest.NewRecorder()
	h.ServeHTTP(wrong, httptest.NewRequest(http.MethodPost, "/api/rooms/"+created.Code+"/join", bytes.NewBufferString(`{"name":"Alice","password":"wrong"}`)))
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d, want 401", wrong.Code)
	}

	join := httptest.NewRecorder()
	h.ServeHTTP(join, httptest.NewRequest(http.MethodPost, "/api/rooms/"+created.Code+"/join", bytes.NewBufferString(`{"name":"Alice","password":"secret"}`)))
	if join.Code != http.StatusOK {
		t.Fatalf("join status=%d body=%s", join.Code, join.Body.String())
	}
}
