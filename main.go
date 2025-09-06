package main

import (
	"os"
	"os/signal"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/yz4230/tinyhost-poc/mdns"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})

	server := mdns.NewServer([]string{"hello.local."})
	if err := server.Start(); err != nil {
		panic(err)
	}
	defer server.Stop()

	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, os.Interrupt)
	<-chSignal
}
