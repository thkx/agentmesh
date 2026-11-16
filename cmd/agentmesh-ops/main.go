// Command agentmesh-ops provides authenticated local service status and shutdown controls.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args)<2 { usage();os.Exit(2) }
	fs:=flag.NewFlagSet(os.Args[1],flag.ExitOnError)
	base:=fs.String("url","http://127.0.0.1:8080","AgentMesh base URL")
	timeout:=fs.Duration("timeout",5*time.Second,"request timeout")
	_=fs.Parse(os.Args[2:])
	token:=strings.TrimSpace(os.Getenv("AGENTMESH_API_TOKEN"))
	if token=="" { fatal("AGENTMESH_API_TOKEN is required") }
	endpoint:=strings.TrimRight(*base,"/")
	method:=http.MethodGet
	switch os.Args[1] {
	case "status": endpoint+="/api/v1/operations/health"
	case "shutdown": method=http.MethodPost;endpoint+="/api/v1/operations/shutdown"
	default: usage();os.Exit(2)
	}
	ctx,cancel:=context.WithTimeout(context.Background(),*timeout);defer cancel()
	req,err:=http.NewRequestWithContext(ctx,method,endpoint,nil);if err!=nil{fatal(err.Error())}
	req.Header.Set("Authorization","Bearer "+token)
	client:=&http.Client{Timeout:*timeout}
	resp,err:=client.Do(req);if err!=nil{fatal("request failed: "+err.Error())};defer resp.Body.Close()
	var payload map[string]any
	if err=json.NewDecoder(resp.Body).Decode(&payload);err!=nil{fatal("invalid operations response")}
	encoded,_:=json.MarshalIndent(payload,"","  ");fmt.Println(string(encoded))
	if resp.StatusCode<200||resp.StatusCode>=300 { os.Exit(1) }
}
func usage(){fmt.Fprintln(os.Stderr,"Usage: agentmesh-ops <status|shutdown> [--url http://127.0.0.1:8080] [--timeout 5s]\nSet AGENTMESH_API_TOKEN in the environment.")}
func fatal(message string){fmt.Fprintln(os.Stderr,"agentmesh-ops:",message);os.Exit(1)}
