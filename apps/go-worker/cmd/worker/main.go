package main

import (
	"log"
	"os"

	"github.com/jroell/osint-agent/apps/go-worker/internal/server"
)

func main() {
	e := server.NewServer()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	log.Printf("go-worker listening on :%s", port)
	if err := e.Start(":" + port); err != nil {
		log.Fatal(err)
	}
}
