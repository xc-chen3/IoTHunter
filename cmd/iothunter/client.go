package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	core "github.com/iothunter/iothunter/internal/core"
)

type apiClient struct {
	baseURL string
	http    *http.Client
}

func newAPIClient(baseURL string) *apiClient {
	return &apiClient{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *apiClient) request(method, path string, input any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if len(data) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		_, _ = os.Stdout.Write(data)
		_, _ = os.Stdout.Write([]byte("\n"))
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func client(args []string) {
	if len(args) == 0 {
		clientUsage()
		return
	}
	server := "http://127.0.0.1:8080"
	if len(args) >= 2 && args[0] == "--server" {
		server, args = args[1], args[2:]
	}
	if len(args) == 0 {
		clientUsage()
		return
	}
	api, command := newAPIClient(server), args[0]
	args = args[1:]
	var err error
	switch command {
	case "health":
		err = api.request(http.MethodGet, "/healthz", nil)
	case "capabilities":
		err = api.request(http.MethodGet, "/api/v1/capabilities", nil)
	case "tools":
		err = api.request(http.MethodGet, "/api/v1/tools", nil)
	case "workspaces":
		err = api.request(http.MethodGet, "/api/v1/workspaces", nil)
	case "workspace":
		err = clientWorkspace(api, args)
	case "workspace-create":
		err = clientWorkspaceCreate(api, args)
	case "target-create":
		err = clientTargetCreate(api, args)
	case "run":
		err = clientRun(api, args)
	default:
		clientUsage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "client:", err)
		os.Exit(1)
	}
}

func clientWorkspace(api *apiClient, args []string) error {
	fs := flag.NewFlagSet("client workspace", flag.ContinueOnError)
	id := fs.String("id", "", "workspace id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("-id is required")
	}
	return api.request(http.MethodGet, "/api/v1/workspaces/"+*id, nil)
}

func clientWorkspaceCreate(api *apiClient, args []string) error {
	fs := flag.NewFlagSet("client workspace-create", flag.ContinueOnError)
	name := fs.String("name", "", "workspace name")
	owner := fs.String("owner", "local", "workspace owner")
	description := fs.String("description", "", "workspace description")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("-name is required")
	}
	return api.request(http.MethodPost, "/api/v1/workspaces", map[string]string{"name": *name, "owner": *owner, "description": *description})
}

func clientTargetCreate(api *apiClient, args []string) error {
	fs := flag.NewFlagSet("client target-create", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace id")
	name := fs.String("name", "", "target name")
	vendor := fs.String("vendor", "", "target vendor")
	model := fs.String("model", "", "target model")
	address := fs.String("address", "", "target address or identifier")
	transport := fs.String("transport", "", "target transport")
	authorized := fs.Bool("authorized", false, "mark target as authorized")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" || *name == "" {
		return fmt.Errorf("-workspace and -name are required")
	}
	return api.request(http.MethodPost, "/api/v1/workspaces/"+*workspace+"/targets", core.Target{Name: *name, Vendor: *vendor, Model: *model, Address: *address, Transport: *transport, Authorized: *authorized})
}

func clientRun(api *apiClient, args []string) error {
	fs := flag.NewFlagSet("client run", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace id")
	target := fs.String("target", "", "target id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("-workspace is required")
	}
	input := map[string]string{}
	if *target != "" {
		input["target_id"] = *target
	}
	return api.request(http.MethodPost, "/api/v1/workspaces/"+*workspace+"/run", input)
}

func clientUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  iothunter client [--server URL] health")
	fmt.Fprintln(os.Stderr, "  iothunter client [--server URL] capabilities|tools|workspaces")
	fmt.Fprintln(os.Stderr, "  iothunter client [--server URL] workspace-create --name NAME [--owner OWNER]")
	fmt.Fprintln(os.Stderr, "  iothunter client [--server URL] workspace --id W-...")
	fmt.Fprintln(os.Stderr, "  iothunter client [--server URL] target-create --workspace W-... --name NAME --authorized")
	fmt.Fprintln(os.Stderr, "  iothunter client [--server URL] run --workspace W-... [--target T-...]")
}
