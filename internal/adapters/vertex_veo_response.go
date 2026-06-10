package adapters

import (
	"fmt"
	"strings"
)

type vertexOperation struct {
	Name     string           `json:"name"`
	Done     bool             `json:"done"`
	Error    *vertexError     `json:"error,omitempty"`
	Response *vertexVeoResult `json:"response,omitempty"`
}

type vertexError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// message は Vertex AI オペレーションエラーを短い診断文字列に整形します。
func (e vertexError) message() string {
	parts := []string{e.Status, e.Message}
	if e.Code != 0 {
		parts = append([]string{fmt.Sprintf("code=%d", e.Code)}, parts...)
	}
	nonEmpty := parts[:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, ": ")
}

type vertexVeoResult struct {
	Videos          []vertexVideo          `json:"videos,omitempty"`
	GeneratedVideos []vertexGeneratedVideo `json:"generatedVideos,omitempty"`
}

type vertexGeneratedVideo struct {
	Video vertexVideo `json:"video"`
}

type vertexVideo struct {
	GCSURI    string `json:"gcsUri,omitempty"`
	URI       string `json:"uri,omitempty"`
	MimeType  string `json:"mimeType,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// firstGeneratedVideo は既知の Vertex AI Veo レスポンス形式から最初の生成動画を取り出します。
func firstGeneratedVideo(op *vertexOperation) (vertexVideo, error) {
	if op.Response == nil {
		return vertexVideo{}, fmt.Errorf("Veo operation response is empty")
	}
	if len(op.Response.Videos) > 0 {
		video := op.Response.Videos[0]
		if video.GCSURI == "" {
			video.GCSURI = video.URI
		}
		return video, nil
	}
	if len(op.Response.GeneratedVideos) > 0 {
		video := op.Response.GeneratedVideos[0].Video
		if video.GCSURI == "" {
			video.GCSURI = video.URI
		}
		return video, nil
	}
	return vertexVideo{}, fmt.Errorf("Veo operation response contains no videos")
}
