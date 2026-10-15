// Command local runs the API with an in-memory store, in-memory uploads and a
// scripted stand-in for Claude, so the UI can be built and demoed without AWS.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"helperpay/agent"
	"helperpay/api"
	"helperpay/pricing"
	"helperpay/records"
)

type localInvoker struct{ run func(id string) }

func (l localInvoker) Invoke(_ context.Context, id string) error { go l.run(id); return nil }

type localUploads struct {
	base  string
	blobs *agent.LocalBlobs
}

func (u localUploads) PresignPut(_ context.Context, key, _ string) (string, error) {
	return u.base + "/blob/" + key, nil
}
func (u localUploads) Wipe(_ context.Context) error { u.blobs.Reset(); return nil }
func (u localUploads) PresignGet(_ context.Context, key string) (string, error) {
	return u.base + "/blob/" + key, nil
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8787"
	}
	base := "http://localhost:" + port
	st := records.NewMemStore()
	loc := pricing.NY()
	m := &records.Machine{Store: st, Loc: loc, Now: time.Now}
	blobs := agent.NewLocalBlobs()
	mem := agent.NewLocalMemory()
	deps := agent.Deps{Store: st, Machine: m, Blobs: blobs, Memory: mem, LLM: agent.ScriptedLLM{}, Loc: loc, Now: time.Now, Actor: "jordan"}
	srv := &api.Server{Store: st, Machine: m, Uploads: localUploads{base, blobs}, Memory: mem, Now: time.Now, StaleAfter: 5 * time.Minute,
		Invoker: localInvoker{run: func(id string) {
			time.Sleep(400 * time.Millisecond)  // let the UI see "queued"
			runner := &agent.Runner{Deps: deps} // one runner per turn: its card list is per turn
			if err := runner.RunTurn(context.Background(), id); err != nil {
				log.Printf("turn %s: %v", id, err)
			}
		}}}

	mux := http.NewServeMux()
	mux.Handle("/blob/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,PUT,OPTIONS")
		key := strings.TrimPrefix(r.URL.Path, "/blob/")
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(204)
		case http.MethodPut:
			data, _ := io.ReadAll(io.LimitReader(r.Body, 10<<20))
			blobs.Put(key, data, r.Header.Get("Content-Type"))
			w.WriteHeader(200)
		case http.MethodGet:
			data, ct, err := blobs.Get(r.Context(), key)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", ct)
			_, _ = w.Write(data)
		}
	}))
	mux.Handle("/config.json", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(map[string]any{"apiUrl": base, "local": true})
	}))
	mux.Handle("/", srv.Handler())
	fmt.Printf("helper-pay local API on %s (scripted assistant, in-memory store)\n", base)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
