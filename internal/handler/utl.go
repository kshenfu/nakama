package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
)

var errStreamingUnsupported = errors.New("streaming unsupported")

func respond(w http.ResponseWriter, v interface{}, statusCode int) {
	b, err := json.Marshal(v)
	if err != nil {
		respondErr(w, fmt.Errorf("could not marshal response: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write(b)
}

func respondErr(w http.ResponseWriter, err error) {
	log.Println(err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func writeSSE(w io.Writer, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("could not marshal response: %v\n", err)
		fmt.Fprintf(w, "event: error\ndata: %v\n\n", err)
		return
	}

	fmt.Fprintf(w, "data: %s\n\n", b)
}
