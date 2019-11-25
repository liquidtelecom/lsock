// TCP Socket Handling Library
// (c) 2019 Liquid Telecommunications - Please see LICENSE for licensing rights

/*
Basic logging functionality
*/

package lsock
import (
	"os"
	"log"
)

func DebugLog(msg string) {
	var f *os.File
	var err error

	if !DEBUG_ENABLED {
		return
	}
	if LOG_TO_FILE {
		if f, err = os.OpenFile(LOG_FILE, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666); err != nil {
			log.SetOutput(os.Stdout)
			log.Fatalf("Error opening log file: %v\n", err)
		}
		defer f.Close()
		log.SetOutput(f)
	}
	log.Printf("%s\n",msg)
	return
}

