package models

import "time"

type Room struct {
	code         string
	passwordHash []byte
	createdAt    time.Time
	lastActivity time.Time
	members      map[string]*Client
}
