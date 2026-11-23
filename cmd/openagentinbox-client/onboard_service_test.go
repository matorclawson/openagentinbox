package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestWorkerDefinitions(t *testing.T) {
	const exe = "/Users/example/Space & Tools/client"
	const origin = "https://inbox.example"
	_, _, body, err := workerDefinition("darwin", "/Users/example", exe, origin)
	var config struct {
		Dict struct {
			Array struct {
				Args []string `xml:"string"`
			} `xml:"array"`
		} `xml:"dict"`
	}
	if err != nil || xml.Unmarshal([]byte(body), &config) != nil || strings.Join(config.Dict.Array.Args, "|") != exe+"|onboard|"+origin+"|--watch" {
		t.Fatal("invalid launch agent arguments")
	}
	id, path, body, err := workerDefinition("linux", "/home/example", "/opt/50%/$user/client", origin)
	if err != nil || !strings.HasSuffix(path, id) || !strings.Contains(body, `ExecStart="/opt/50%%/$$user/client" onboard "https://inbox.example" --watch`) {
		t.Fatal("invalid systemd user service")
	}
}
