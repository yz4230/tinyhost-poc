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

	responder := mdns.NewResponder([]string{"hello.local."})
	if err := responder.Start(); err != nil {
		panic(err)
	}
	defer responder.Stop()

	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, os.Interrupt)
	<-chSignal
}
