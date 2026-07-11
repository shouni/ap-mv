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
	var parts []string
	if e.Code != 0 {
		parts = append(parts, fmt.Sprintf("code=%d", e.Code))
	}
	if s := strings.TrimSpace(e.Status); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(e.Message); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, ": ")
}

type vertexVeoResult struct {
	Videos                  []vertexVideo          `json:"videos,omitempty"`
	GeneratedVideos         []vertexGeneratedVideo `json:"generatedVideos,omitempty"`
	RaiMediaFilteredCount   int                    `json:"raiMediaFilteredCount,omitempty"`
	RaiMediaFilteredReasons []string               `json:"raiMediaFilteredReasons,omitempty"`
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
		return vertexVideo{}, fmt.Errorf("veo operation response is empty")
	}
	if len(op.Response.Videos) > 0 {
		return normalizeVideoGCSURI(op.Response.Videos[0]), nil
	}
	if len(op.Response.GeneratedVideos) > 0 {
		return normalizeVideoGCSURI(op.Response.GeneratedVideos[0].Video), nil
	}
	if op.Response.RaiMediaFilteredCount > 0 || len(op.Response.RaiMediaFilteredReasons) > 0 {
		reasons := strings.Join(op.Response.RaiMediaFilteredReasons, "; ")
		return vertexVideo{}, fmt.Errorf("veo operation response contains no videos: filtered by content safety policy (count=%d, reasons=%s)", op.Response.RaiMediaFilteredCount, reasons)
	}
	return vertexVideo{}, fmt.Errorf("veo operation response contains no videos")
}

// normalizeVideoGCSURI fills GCSURI from URI when the response omitted the former.
func normalizeVideoGCSURI(video vertexVideo) vertexVideo {
	if video.GCSURI == "" {
		video.GCSURI = video.URI
	}
	return video
}
