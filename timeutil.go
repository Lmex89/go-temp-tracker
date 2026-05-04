package main

import (
	"time"
)

type TimeConverter interface {
	ToLocal(utcTimestamp string) string
}

type MeridaTimeConverter struct {
	location *time.Location
}

func NewMeridaTimeConverter() *MeridaTimeConverter {
	loc, err := time.LoadLocation("America/Merida")
	if err != nil {
		Logger.Error("Failed to load America/Merida timezone: %v", err)
		loc = time.UTC
	}
	return &MeridaTimeConverter{location: loc}
}

func (c *MeridaTimeConverter) ToLocal(utcTimestamp string) string {
	utcTime, err := time.Parse("2006-01-02 15:04:05", utcTimestamp)
	if err != nil {
		return utcTimestamp
	}
	return utcTime.In(c.location).Format("2006-01-02 15:04:05")
}