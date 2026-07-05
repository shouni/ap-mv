package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shouni/ap-mv/internal/ports"
)

// startOperation は Vertex AI に predictLongRunning リクエストを送信し、操作ハンドルを返します。
func (r *VertexVeoRunner) startOperation(ctx context.Context, req ports.VideoGenerationRequest) (*vertexOperation, error) {
	var op vertexOperation
	if err := r.postJSON(ctx, r.modelURL("predictLongRunning"), r.buildGenerateBody(ctx, req), &op); err != nil {
		return nil, fmt.Errorf("start Veo operation: %w", err)
	}
	if strings.TrimSpace(op.Name) == "" {
		return nil, fmt.Errorf("start Veo operation: response missing operation name")
	}
	return &op, nil
}

// waitOperation は Veo の長時間実行オペレーションが完了するか context が終了するまで Vertex AI をポーリングします。
func (r *VertexVeoRunner) waitOperation(ctx context.Context, operationName string) (*vertexOperation, error) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		var op vertexOperation
		body := map[string]string{"operationName": operationName}
		if err := r.postJSON(ctx, r.modelURL("fetchPredictOperation"), body, &op); err != nil {
			consecutiveErrors++
			if consecutiveErrors >= r.maxPollConsecutiveErrors {
				return nil, fmt.Errorf("fetch Veo operation failed consecutively %d times: %w", consecutiveErrors, err)
			}
		} else {
			consecutiveErrors = 0
			if op.Done {
				if op.Error != nil {
					return nil, fmt.Errorf("veo operation failed: %s", op.Error.message())
				}
				return &op, nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// postJSON は JSON POST リクエストを送信し、JSON レスポンスを out にデコードします。
func (r *VertexVeoRunner) postJSON(ctx context.Context, url string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// modelURL は指定された Veo メソッド用の Vertex AI Publisher Model エンドポイント URL を組み立てます。
func (r *VertexVeoRunner) modelURL(method string) string {
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
		r.locationID, r.projectID, r.locationID, r.model, method)
}
