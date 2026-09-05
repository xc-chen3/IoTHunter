package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	core "github.com/iothunter/iothunter/internal/core"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "demo":
		demo(os.Args[2:])
	case "report":
		report(os.Args[2:])
	case "capabilities":
		capabilities(os.Args[2:])
	case "client":
		client(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func storeFrom(fs *flag.FlagSet) *string {
	return fs.String("data", ".iothunter/state.json", "state file path")
}
func openEngine(path string) *core.Engine {
	s, err := core.NewStore(path)
	if err != nil {
		log.Fatal(err)
	}
	return core.NewEngine(s, 4)
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address")
	data := storeFrom(fs)
	_ = fs.Parse(args)
	e := openEngine(*data)
	log.Printf("IoTHunter listening on %s (state: %s)", *addr, *data)
	if err := http.ListenAndServe(*addr, core.NewAPIServer(e).Handler()); err != nil {
		log.Fatal(err)
	}
}

func demo(args []string) {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	data := storeFrom(fs)
	_ = fs.Parse(args)
	e := openEngine(*data)
	w, err := e.CreateWorkspace("Demo workspace", "local", "A safe, passive demonstration of the IoTHunter research loop.")
	if err != nil {
		log.Fatal(err)
	}
	t, err := e.CreateTarget(w.ID, core.Target{Name: "Lab gateway", Vendor: "ExampleVendor", Model: "ExampleRouter X1", Address: "192.0.2.10", Transport: "http", Authorized: true})
	if err != nil {
		log.Fatal(err)
	}
	task, err := e.SubmitResearch(context.Background(), w.ID, t.ID)
	if err != nil {
		log.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := e.Store.Task(task.ID)
		if ok && (current.Status == core.TaskCompleted || current.Status == core.TaskFailed || current.Status == core.TaskBlocked) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	path, err := e.GenerateReport(w.ID)
	if err != nil {
		log.Fatal(err)
	}
	out := map[string]any{"workspace": w, "target": t, "task": mustTask(e, task.ID), "findings": filterFindings(e.Store.Snapshot().Findings, w.ID), "report": path}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func report(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	data := storeFrom(fs)
	workspace := fs.String("workspace", "", "workspace id")
	_ = fs.Parse(args)
	if *workspace == "" {
		log.Fatal("-workspace is required")
	}
	e := openEngine(*data)
	path, err := e.GenerateReport(*workspace)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(path)
}
func capabilities(args []string) {
	fs := flag.NewFlagSet("capabilities", flag.ExitOnError)
	data := storeFrom(fs)
	_ = fs.Parse(args)
	e := openEngine(*data)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(e.Capabilities())
}
func mustTask(e *core.Engine, id string) core.Task { t, _ := e.Store.Task(id); return t }
func filterFindings(in []core.Finding, id string) []core.Finding {
	out := []core.Finding{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func usage() {
	fmt.Fprint(os.Stderr, "IoTHunter\n\nUsage:\n  iothunter serve [--addr :8080] [--data .iothunter/state.json]\n  iothunter demo [--data .iothunter/state.json]\n  iothunter report --workspace W-... [--data .iothunter/state.json]\n  iothunter capabilities\n  iothunter client --server http://127.0.0.1:8080 health|workspaces|run|...\n")
}
