package main

import (
	"encoding/json"
	"io"
	"net/http"
)

// sttHandler is the demo's stand-in speech service, in the form whisper.cpp's whisper-server takes
// (POST /inference, the audio as the multipart field "file", the answer as {"text": ...}); the
// OpenAI-compatible path answers the same. It only knows the demo's own lines: any other sound is
// written out as nothing.
func sttHandler() http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "send the audio as the multipart field file", http.StatusBadRequest)
			return
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		pcm, rate, err := readWAV(b)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		text := ""
		if k := whichLine(pcm, rate); k >= 0 {
			text, _ = lineAt(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
	}
	mux.HandleFunc("POST /inference", write)
	mux.HandleFunc("POST /v1/audio/transcriptions", write)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"status":"ok"}`) })
	return mux
}
