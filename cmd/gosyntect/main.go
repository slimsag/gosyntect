package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sourcegraph/gosyntect"
)

var scopifyFlag = flag.Bool("scopify", false, "print scopified file regions instead of requesting highlighted HTML")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `usage: gosyntect [-scopify] <server> <theme?> <file>

  Highlight file to HTML:
  	gosyntect <server> <theme> <file>
  	gosyntect http://localhost:9238 'InspiredGitHub' gosyntect.go

  Scopify file as JSON:
  	gosyntect -scopify <server> <file>
  	gosyntect -scopify http://localhost:9238 gosyntect.go

`)
	}
	flag.Parse()
	log.SetFlags(0)
	log.SetPrefix("gosyntect: ")
	if !*scopifyFlag && flag.NArg() != 3 || *scopifyFlag && flag.NArg() != 2 {
		flag.PrintDefaults()
		os.Exit(2)
	}

	// Validate server argument.
	server := flag.Arg(0)
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		log.Fatal("expected server to have http:// or https:// prefix")
	}

	query := &gosyntect.Query{
		Scopify: *scopifyFlag,
	}

	if !*scopifyFlag {
		// Validate theme argument.
		query.Theme = flag.Arg(1)
		if query.Theme == "" {
			log.Fatal("theme argument is required (e.x. 'InspiredGitHub')")
		}

		// Validate file argument.
		query.Filepath = flag.Arg(2)
		data, err := ioutil.ReadFile(query.Filepath)
		if err != nil {
			log.Fatal(err)
		}
		query.Code = string(data)
	} else {
		// Validate file argument.
		query.Filepath = flag.Arg(1)
		data, err := ioutil.ReadFile(query.Filepath)
		if err != nil {
			log.Fatal(err)
		}
		query.Code = string(data)
	}
	query.Filepath = filepath.Base(query.Filepath)

	cl := gosyntect.New(server)
	resp, err := cl.Highlight(context.Background(), query)
	if err != nil {
		log.Fatal(err)
	}
	if resp.Data != "" {
		fmt.Println(resp.Data)
	} else {
		for _, region := range resp.ScopifiedRegions {
			var scopes []string
			for _, index := range region.Scopes {
				scopes = append(scopes, resp.ScopifiedScopeNames[index])
			}
			fmt.Printf("%q - %s\n", query.Code[region.Offset:region.Offset+region.Length], strings.Join(scopes, " "))
		}
	}
}
