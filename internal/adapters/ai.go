// Package adapters provides the adapter implementations that initialise the Vertex AI client and
// convert requests/responses for the Veo video-generation API.
package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/ap-mv/internal/config"
)

const (
	// defaultVertexLocationID is Vertex AI's default location
	defaultVertexLocationID = "global"
	// defaultVertexInitialDelay is the retry delay
	defaultVertexInitialDelay = 60 * time.Second
)

// NewVertexAIAdapter initialises the GCP Vertex AI client.
func NewVertexAIAdapter(ctx context.Context, ai *config.Config) (*gemini.Client, error) {
	if ai.GCP.ProjectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID is not set")
	}

	clientConfig := gemini.Config{
		ProjectID:    ai.GCP.ProjectID,
		LocationID:   vertexLocationID(ai),
		InitialDelay: defaultVertexInitialDelay,
	}

	aiClient, err := gemini.New(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialise the Vertex AI client: %w", err)
	}

	return aiClient, nil
}

// vertexLocationID resolves the location with the same priority as the video-generation location
// logic (VEO_LOCATION_ID → GCP_LOCATION_ID). Only when neither is set does it fall back to
// defaultVertexLocationID. This keeps the Vertex AI client for text/image generation in the same
// region as video generation actually uses, as far as possible.
func vertexLocationID(ai *config.Config) string {
	if locationID := strings.TrimSpace(ai.AI.VeoLocationID); locationID != "" {
		return locationID
	}
	if locationID := strings.TrimSpace(ai.GCP.LocationID); locationID != "" {
		return locationID
	}
	return defaultVertexLocationID
}
