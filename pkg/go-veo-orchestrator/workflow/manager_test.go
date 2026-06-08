package workflow

import (
	"context"
	"io"
	"net/http"
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
	"google.golang.org/genai"
)

func TestNewBuildsWorkflows(t *testing.T) {
	workflows, err := New(testManagerArgs())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if workflows.Script == nil {
		t.Fatal("Script runner is nil")
	}
	if workflows.CutKeyframe == nil {
		t.Fatal("CutKeyframe runner is nil")
	}
	if workflows.Publish == nil {
		t.Fatal("Publish runner is nil")
	}
	if workflows.Video != nil {
		t.Fatal("Video runner should be nil without VideoRunner dependency")
	}
}

func testManagerArgs() ManagerArgs {
	chars, err := characterkit.ParseCharacters([]byte(`[
		{
			"id": "main",
			"name": "Main",
			"visual_cues": ["blue jacket"],
			"reference_url": "gs://bucket/main.png",
			"is_default": true
		}
	]`))
	if err != nil {
		panic(err)
	}

	return ManagerArgs{
		Config: ports.Config{
			GeminiModel: "gemini-text",
			ImageModel:  "gemini-image",
		},
		HTTPClient: fakeHTTPClient{},
		Reader:     fakeContentReader{},
		Writer:     fakeWriter{},
		AIClient:   fakeGenerativeModel{},
		PromptDeps: &PromptDeps{
			Characters:     chars,
			ScriptPrompt:   fakeScriptPrompt{},
			KeyframePrompt: fakeKeyframePrompt{},
		},
	}
}

type fakeGenerativeModel struct{}

func (fakeGenerativeModel) GenerateContent(context.Context, string, string) (*gemini.Response, error) {
	return &gemini.Response{}, nil
}

func (fakeGenerativeModel) GenerateWithParts(context.Context, string, []*genai.Part, gemini.GenerateOptions) (*gemini.Response, error) {
	return &gemini.Response{}, nil
}

func (fakeGenerativeModel) IsVertexAI() bool {
	return false
}

func (fakeGenerativeModel) UploadFile(context.Context, io.Reader, string, string) (string, string, error) {
	return "file", "uri", nil
}

func (fakeGenerativeModel) DeleteFile(context.Context, string) error {
	return nil
}

type fakeHTTPClient struct{}

func (fakeHTTPClient) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody)}, nil
}

func (fakeHTTPClient) DoRequest(*http.Request) ([]byte, error) {
	return nil, nil
}

func (fakeHTTPClient) FetchBytes(context.Context, string) ([]byte, error) {
	return nil, nil
}

func (fakeHTTPClient) FetchAndDecodeJSON(context.Context, string, any) error {
	return nil
}

func (fakeHTTPClient) PostJSONAndFetchBytes(context.Context, string, any) ([]byte, error) {
	return nil, nil
}

func (fakeHTTPClient) PostRawBodyAndFetchBytes(context.Context, string, []byte, string) ([]byte, error) {
	return nil, nil
}

func (fakeHTTPClient) FetchStream(context.Context, string, func(io.Reader) error) error {
	return nil
}

func (fakeHTTPClient) GetStream(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(http.NoBody), nil
}

func (fakeHTTPClient) IsSafeURL(string) (bool, error) {
	return true, nil
}

func (fakeHTTPClient) IsSecureServiceURL(string) bool {
	return true
}

type fakeContentReader struct{}

func (fakeContentReader) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(http.NoBody), nil
}

func (fakeContentReader) List(context.Context, string, func(string) error) error {
	return nil
}

func (fakeContentReader) Exists(context.Context, string) (bool, error) {
	return true, nil
}

type fakeWriter struct{}

func (fakeWriter) Write(context.Context, string, io.Reader, ...remoteio.WriteOption) error {
	return nil
}

func (fakeWriter) Delete(context.Context, string) error {
	return nil
}

type fakeScriptPrompt struct{}

func (fakeScriptPrompt) Build(string, *ports.TemplateData) (string, error) {
	return "prompt", nil
}

type fakeKeyframePrompt struct{}

func (fakeKeyframePrompt) BuildCut(ports.Cut, *characterkit.Character) (string, string) {
	return "user", "system"
}
