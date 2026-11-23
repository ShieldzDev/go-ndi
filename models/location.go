package models

import "time"

type location struct {
	Lat      float64   `json:"lat"`
	Lng      float64   `json:"lng"`
	Accuracy float64   `json:"accuracy"`
	TS       time.Time `json:"ts"`
}
