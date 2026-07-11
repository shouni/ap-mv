package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shouni/ap-mv/internal/ports"
)

// rawResponseLogLimit は診断ログに残す生レスポンス本文の最大バイト数です。
const rawResponseLogLimit = 4000

// startOperation は Vertex AI に predictLongRunning リクエストを送信し、操作ハンドルを返します。
func (r *VertexVeoRunner) startOperation(ctx context.Context, req ports.VideoGenerationRequest) (*vertexOperation, error) {
	var op vertexOperation
	if _, err := r.postJSON(ctx, r.modelURL("predictLongRunning"), r.buildGenerateBody(ctx, req), &op); err != nil {
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
		raw, err := r.postJSON(ctx, r.modelURL("fetchPredictOperation"), body, &op)
		if err != nil {
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
				if op.Response == nil || (len(op.Response.Videos) == 0 && len(op.Response.GeneratedVideos) == 0) {
					slog.WarnContext(ctx, "veo operation completed with no videos in response",
						"operation", operationName,
						"raw_response", truncateForLog(raw, rawResponseLogLimit),
					)
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
// 呼び出し元が診断ログに利用できるよう、デコード前の生レスポンス本文も返します。
func (r *VertexVeoRunner) postJSON(ctx context.Context, url string, body any, out any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return respBody, nil
}

// truncateForLog はログ出力用に本文を上限バイト数まで切り詰めます。
func truncateForLog(body []byte, limit int) string {
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "...(truncated)"
}

// modelURL は指定された Veo メソッド用の Vertex AI Publisher Model エンドポイント URL を組み立てます。
// global エンドポイントはリージョンエンドポイントと異なりホスト名にロケーションプレフィックスを付けません
// （https://aiplatform.googleapis.com/v1/projects/{p}/locations/global/...）。
func (r *VertexVeoRunner) modelURL(method string) string {
	host := r.locationID + "-aiplatform.googleapis.com"
	if r.locationID == "global" {
		host = "aiplatform.googleapis.com"
	}
	return fmt.Sprintf("https://%s/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
		host, r.projectID, r.locationID, r.model, method)
}
