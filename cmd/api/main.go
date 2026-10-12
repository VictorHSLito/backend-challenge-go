package main

import (
	"log"

	"go.uber.org/fx"

	"github.com/joho/godotenv"
	"github.com/victorhslito/junglegaming/internal/app"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("warnig: .env not found: %v", err)
	}
	fx.New(app.Options()).Run()
}
