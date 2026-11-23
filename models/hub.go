package models

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	maxMembers   = 10
	emptyRoomTTL = 10 * time.Minute
	roomTTL      = 12 * time.Hour
)

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

type memberView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Lat         float64   `json:"lat,omitempty"`
	Lng         float64   `json:"lng,omitempty"`
	Accuracy    float64   `json:"accuracy,omitempty"`
	TS          time.Time `json:"ts,omitempty"`
	HasLocation bool      `json:"hasLocation"`
}

func NewHub() *Hub { return &Hub{rooms: make(map[string]*Room)} }

func nonblockingSend(c *Client, msg []byte) {
	select {
	case c.send <- msg:
	default:
		// A full queue means this peer is no longer keeping up; close it so it unregisters.
		_ = c.conn.Close(websocket.StatusPolicyViolation, "client too slow")
	}
}

func (h *Hub) create(passwordHash []byte) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for attempts := 0; attempts < 8; attempts++ {
		var random [6]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		code := make([]byte, len(random))
		for i, b := range random {
			code[i] = alphabet[int(b)%len(alphabet)]
		}
		now := time.Now()
		h.mu.Lock()
		if _, exists := h.rooms[string(code)]; !exists {
			h.rooms[string(code)] = &Room{code: string(code),
				passwordHash: passwordHash,
				createdAt:    now,
				lastActivity: now,
				members:      make(map[string]*Client)}
			h.mu.Unlock()
			return string(code), nil
		}
		h.mu.Unlock()
	}
	return "", errors.New("could not generate a unique room code")
}

func (h *Hub) status(code string) (exists, full, needsPassword bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.rooms[code]
	if !ok {
		return false, false, false
	}
	return true, len(r.members) >= maxMembers, len(r.passwordHash) > 0
}

func (h *Hub) register(code string, c *Client) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[code]
	if !ok {
		return nil, errors.New("room_not_found")
	}
	if _, exists := r.members[c.id]; exists {
		return nil, errors.New("already_connected")
	}
	if len(r.members) >= maxMembers {
		return nil, errors.New("room_full")
	}
	members := make([]memberView, 0, len(r.members)+1)
	for _, member := range r.members {
		view := memberView{ID: member.id, Name: member.name}
		if member.last != nil {
			view.Lat, view.Lng, view.Accuracy, view.TS, view.HasLocation = member.last.Lat, member.last.Lng, member.last.Accuracy, member.last.TS, true
		}
		members = append(members, view)
	}
	members = append(members, memberView{ID: c.id, Name: c.name})
	initMsg, _ := json.Marshal(map[string]any{"type": "init", "data": map[string]any{"selfId": c.id, "members": members}})
	c.send <- initMsg
	r.members[c.id] = c
	r.lastActivity = time.Now()
	joined, _ := json.Marshal(map[string]any{"type": "member_joined", "data": memberView{ID: c.id, Name: c.name}})
	for id, member := range r.members {
		if id != c.id {
			nonblockingSend(member, joined)
		}
	}
	return nil, nil
}

func (h *Hub) unregister(code, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[code]
	if !ok {
		return
	}
	if _, ok := r.members[id]; !ok {
		return
	}
	delete(r.members, id)
	r.lastActivity = time.Now()
	msg, _ := json.Marshal(map[string]any{"type": "member_left", "data": map[string]string{"id": id}})
	for _, member := range r.members {
		nonblockingSend(member, msg)
	}
}

func (h *Hub) update(code string, c *Client, loc location) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[code]
	if !ok || r.members[c.id] != c {
		return
	}
	loc.TS = time.Now().UTC()
	c.last = &loc
	r.lastActivity = loc.TS
	msg, _ := json.Marshal(map[string]any{"type": "location", "data": map[string]any{"id": c.id, "name": c.name, "lat": loc.Lat, "lng": loc.Lng, "accuracy": loc.Accuracy, "ts": loc.TS}})
	for id, member := range r.members {
		if id != c.id {
			nonblockingSend(member, msg)
		}
	}
}

func (h *Hub) janitor(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for code, r := range h.rooms {
		age := now.Sub(r.lastActivity)
		if (len(r.members) == 0 && age > emptyRoomTTL) || age > roomTTL {
			for _, member := range r.members {
				_ = member.conn.Close(websocket.StatusGoingAway, "room expired")
			}
			delete(h.rooms, code)
		}
	}
}

func (h *Hub) runJanitor(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.janitor(now)
		}
	}
}
