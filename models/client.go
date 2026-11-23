package models

import (
	"time"

	"github.com/coder/websocket"
)

type Client struct {
	id       string
	name     string
	conn     *websocket.Conn
	send     chan []byte
	last     *location
	lastSent time.Time
}
