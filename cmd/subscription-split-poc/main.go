package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/poc"
)

func main() {
	upstream := os.Getenv("UPSTREAM_SUBSCRIPTION_URL")
	if upstream == "" {
		log.Fatal("UPSTREAM_SUBSCRIPTION_URL is required")
	}
	if _, err := logger.Init("", "error"); err != nil {
		log.Fatal("failed to initialize logger")
	}
	s := poc.New(upstream)
	s.Seed("A", 10*time.Minute)
	s.Seed("B", 20*time.Minute)
	s.Seed("C", 30*time.Minute)
	fmt.Println("POC pages:")
	fmt.Println(poc.LANBaseURL + "/poc/A")
	fmt.Println(poc.LANBaseURL + "/poc/B")
	fmt.Println(poc.LANBaseURL + "/poc/C")
	fmt.Println("Subscription URLs:")
	fmt.Println(poc.LANBaseURL + "/sub/A")
	fmt.Println(poc.LANBaseURL + "/sub/B")
	fmt.Println(poc.LANBaseURL + "/sub/C")
	log.Fatal(http.ListenAndServe("0.0.0.0:8890", s))
}
