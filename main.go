package main

import (
	"encoding/json"
	"fmt"
	"github.com/Ghvstcode/TCP-Proxy/pkg/server"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
)

type Config struct {
	Apps []server.App
}

func main() {
	// Read in config file
	configEnv := "config.json"

	// If a config file path is provided use it instead
	if len(os.Args) > 1 {
		configEnv = os.Args[1]
	}

	var cfg Config
	data, err := ioutil.ReadFile(configEnv)
	if err != nil {
		log.Fatalf("error reading config file: %v", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("error loading config: %v", err)
	}

	s := server.NewServer(cfg.Apps)

	signal.Notify(s.Quit.C, os.Interrupt)
	signal.Notify(s.Quit.C, os.Kill)

	sig := <-s.Quit.C
	log.Println("Received terminate, graceful shutdown! Signal: ", sig)
	s.Stop()

}
