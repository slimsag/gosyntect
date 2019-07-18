package gosyntect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/opentracing-contrib/go-stdlib/nethttp"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
)

// Query represents a code highlighting query to the syntect_server.
type Query struct {
	// Extension is deprecated: use Filepath instead.
	Extension string `json:"extension"`

	// Filepath is the file path of the code. It can be the full file path, or
	// just the name and extension.
	//
	// See: https://github.com/sourcegraph/syntect_server#supported-file-extensions
	Filepath string `json:"filepath"`

	// Theme is the color theme to use for highlighting.
	//
	// Not used when Scopify == true.
	//
	// See https://github.com/sourcegraph/syntect_server#embedded-themes
	Theme string `json:"theme"`

	// Scopify specifies whether or not to fetch a scopified version of the
	// code instead of highlighteed HTML.
	Scopify bool `json:"scopify"`

	// Code is the literal code to highlight.
	Code string `json:"code"`
}

// Response represents a response to a code highlighting query.
type Response struct {
	// Data is the actual highlighted HTML version of Query.Code.
	//
	// Only present when Query.Scopify was false.
	Data string

	// Plaintext indicates whether or not a syntax could not be found for the
	// file and instead it was rendered as plain text.
	//
	// Only present when Query.Scopify was false.
	Plaintext bool

	// DetectedLanguage tells the name of the language that syntect_server
	// highlighted the code as.
	DetectedLanguage string

	// ScopifiedScopeNames is a mapping from scope name indexes to scope string
	// literals.
	//
	// Only present when Query.Scopify was true.
	ScopifiedScopeNames map[int]string

	// ScopifiedRegion represents a single region of the input Code annotated
	// with scope information from the language grammar.
	//
	// Only present when Query.Scopify was true.
	ScopifiedRegions []ScopifiedRegion
}

// ScopifiedRegion represents a single region of the input Code annotated
// with scope information from the language grammar.
type ScopifiedRegion struct {
	Offset int   // byte offset relative to the input code
	Length int   // length of the region
	Scopes []int // scopes affecting this region according to the language grammar
}

var (
	// ErrInvalidTheme is returned when the Query.Theme is not a valid theme.
	ErrInvalidTheme = errors.New("invalid theme")

	// ErrRequestTooLarge is returned when the request is too large for syntect_server to handle (e.g. file is too large to highlight).
	ErrRequestTooLarge = errors.New("request too large")

	// ErrPanic occurs when syntect_server panics while highlighting code. This
	// most often occurs when Syntect does not support e.g. an obscure or
	// relatively unused sublime-syntax feature and as a result panics.
	ErrPanic = errors.New("syntect panic while highlighting")
)

type response struct {
	// Successful response fields.
	Data                string
	Plaintext           bool
	DetectedLanguage    string            `json:"detected_language"`
	ScopifiedScopeNames []string          `json:"scopified_scope_names"`
	ScopifiedRegions    []ScopifiedRegion `json:"scopified_regions"`

	// Error response fields.
	Error string
	Code  string
}

func (r *response) toSuccessResponse() *Response {
	scopifiedScopeNames := map[int]string{}
	for index, scopeName := range r.ScopifiedScopeNames {
		scopifiedScopeNames[index] = scopeName
	}
	return &Response{
		Data:                r.Data,
		Plaintext:           r.Plaintext,
		DetectedLanguage:    r.DetectedLanguage,
		ScopifiedScopeNames: scopifiedScopeNames,
		ScopifiedRegions:    r.ScopifiedRegions,
	}
}

// Client represents a client connection to a syntect_server.
type Client struct {
	syntectServer string
}

// Highlight performs a query to highlight some code.
func (c *Client) Highlight(ctx context.Context, q *Query) (*Response, error) {
	// Build the request.
	jsonQuery, err := json.Marshal(q)
	if err != nil {
		return nil, errors.Wrap(err, "encoding query")
	}
	req, err := http.NewRequest("POST", c.url("/"), bytes.NewReader(jsonQuery))
	if err != nil {
		return nil, errors.Wrap(err, "building request")
	}
	req.Header.Set("Content-Type", "application/json")

	// Add tracing to the request.
	req = req.WithContext(ctx)
	req, ht := nethttp.TraceRequest(opentracing.GlobalTracer(), req,
		nethttp.OperationName("Highlight"),
		nethttp.ClientTrace(false))
	defer ht.Finish()
	client := &http.Client{Transport: &nethttp.Transport{}}

	// Perform the request.
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("making request to %s", c.url("/")))
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		return nil, ErrRequestTooLarge
	}

	// Can only call ht.Span() after the request has been exected, so add our span tags in now.
	ht.Span().SetTag("Filepath", q.Filepath)
	ht.Span().SetTag("Theme", q.Theme)

	// Decode the response.
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("decoding JSON response from %s", c.url("/")))
	}
	if r.Error != "" {
		var err error
		switch r.Code {
		case "invalid_theme":
			err = ErrInvalidTheme
		case "resource_not_found":
			// resource_not_found is returned in the event of a 404, indicating a bug
			// in gosyntect.
			err = errors.New("gosyntect internal error: resource_not_found")
		case "panic":
			err = ErrPanic
		default:
			err = fmt.Errorf("unknown error=%q code=%q", r.Error, r.Code)
		}
		return nil, errors.Wrap(err, c.syntectServer)
	}
	return r.toSuccessResponse(), nil
}

func (c *Client) url(path string) string {
	return c.syntectServer + path
}

// New returns a client connection to a syntect_server.
func New(syntectServer string) *Client {
	return &Client{
		syntectServer: strings.TrimSuffix(syntectServer, "/"),
	}
}
