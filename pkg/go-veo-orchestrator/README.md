# 🎬 Go Veo Orchestrator

[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-veo-orchestrator)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-veo-orchestrator)](https://github.com/shouni/go-veo-orchestrator/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/shouni/go-veo-orchestrator)](https://goreportcard.com/report/github.com/shouni/go-veo-orchestrator)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-veo-orchestrator.svg)](https://pkg.go.dev/github.com/shouni/go-veo-orchestrator)

## 🚀 概要 (About) - Music Recipe Driven Veo Orchestrator

**Go Veo Orchestrator** は、**Music Recipe（音楽レシピ / 楽曲構成書）** から動画カット列を構造化し、Google の動画生成 AI **Veo (Vertex AI / Gemini API)** へ渡すためのバックエンドオーケストレーターです。

[Gemini Image Kit](https://github.com/shouni/gemini-image-kit) を使ってカットごとのキーフレームを生成し、`VideoRunner` adapter を通じて Veo に **Prompt / Keyframe / Audio / PreviousVideoID / Seed** を渡します。Veo API の具体実装は `ports.VideoRunner` として差し替える設計です。

`video_id` を次カットの `PreviousVideoID` として引き継ぐことで、Video-to-Video の文脈を保った連続カット生成を行います。生成済みカットは `status=generated` と `video_id` / `video_url` を使ってスキップできるため、途中失敗後の再開にも対応しやすい構造です。

---

## ✨ コア・コンセプト (Core Concepts)

* **🧬 Consistency Control**:
  * **キャラクター固有 Seed**、**キーフレーム画像**、**動きのプロンプト**、**前カットの VideoID** を 1 つの `VideoGenerationRequest` にまとめ、カット間の見た目と文脈を維持します。

* **⏳ Audio-Driven Timeline Logic (音楽主導のタイムライン管理)**:
  * Music Recipe の `sections` / `cuts` から `duration_sec`、`start_sec`、`end_sec` を補完し、`audio_cue` を Veo 用プロンプトへ注入します。

* **🔁 Resumable Video Chain**:
  * 各 `cut` は `status`、`video_id`、`video_url` を保持します。生成済みカットは再生成せず、保持済み `video_id` を次カットの `PreviousVideoID` として使用します。

* **🧩 Adapter-Oriented Architecture**:
  * Veo への実通信は `ports.VideoRunner` に閉じ込め、オーケストレーション、キーフレーム生成、メタデータ保存を分離しています。

---

## 🎬 4つの動画生成ワークフロー (Workflows)

| ワークフロー | 担当インターフェース | 内容 |
| --- | --- | --- |
| **1. Scripting** | `ScriptRunner` | 非構造化ドキュメントから、キャラ設定・音楽展開（BGM拍子/Audio Cue）・カット割り・カメラワーク・推定秒数を含む**JSON形式のMusic & Video Recipe**を生成。 |
| **2. Cut Keyframe Gen** | `CutKeyframeRunner` | 各カットのベースとなるキーフレーム画像を、キャラクター Seed と参照画像を使って生成。 |
| **3. Video Gen** | `VideoTimelineRunner` + `VideoRunner` | `VideoRequestBuilder` が `VideoGenerationRequest` を組み立て、Veo adapter に順次投入。 |
| **4. Metadata Publish** | `VideoPublishRunner` | `video_id` / `video_url` / `status` 更新済みの `video_music_meta.json` を保存。 |

---

## 🔌 Adapter Boundary

このリポジトリは Veo API クライアントではなく、Veo に渡すための **ドメインモデル、キーフレーム生成、リクエスト構築、Video-to-Video 連鎖、メタデータ保存** を担当する orchestration ライブラリです。

Veo API への実通信は `ports.VideoRunner` の実装として、利用側アプリケーションまたは別パッケージから差し込みます。このリポジトリ内には本番用 Veo adapter は含めず、実行環境ごとの差分を adapter 側に閉じ込めます。

`VideoRunner` 実装が担う責務は以下です。

* Google Cloud / Vertex AI / Gemini API などの認証
* `ImageReference` / `AudioReference` の解決
* `InputImage` / `InputAudio` を使う場合のアップロードと参照 URI 化
* Veo API への動画生成リクエスト送信
* 長時間 operation のポーリング、タイムアウト、リトライ
* 生成動画の保存先管理
* 次カットへ引き継ぐための `VideoResponse.VideoID` 返却
* 参照可能な `VideoResponse.CloudURL` 返却

adapter 実装では `VideoGenerationRequest.ImageReference` を優先し、空の場合だけ `InputImage` をアップロードして参照 URI を作る想定です。`AudioReference` も同様に、参照 URI がある場合はそれを優先し、必要に応じて `InputAudio` をアップロードします。

`VideoRunner` を指定しない場合、`workflow.New` が返す `Workflows.Video` は `nil` になります。Design / Script / CutKeyframe / Publish だけを使う構成ではそのまま利用できますが、動画生成まで実行する場合は `ManagerArgs.VideoRunner` に実装を渡してください。

```go
type VeoRunner struct {
	// client, bucket, model, location など、実行環境に必要な依存を保持します。
}

func (r *VeoRunner) Run(ctx context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	// 1. req.ImageReference / req.AudioReference を優先して参照を解決
	// 2. 必要なら req.InputImage / req.InputAudio をアップロード
	// 3. req.Prompt, req.PreviousVideoID, req.Seed, req.DurationSec を Veo API に渡す
	// 4. operation を poll して完了を待つ
	// 5. CloudURL と VideoID を返す
	return &ports.VideoResponse{
		CloudURL:    "gs://example-bucket/videos/cut_001.mp4",
		VideoID:     "veo-video-id",
		CutIndex:    req.CutIndex,
		DurationSec: req.DurationSec,
		MimeType:    "video/mp4",
	}, nil
}

workflows, err := workflow.New(workflow.ManagerArgs{
	Config:      cfg,
	HTTPClient:  httpClient,
	Reader:      reader,
	Writer:      writer,
	AIClient:    geminiModel,
	VideoRunner: &VeoRunner{},
	PromptDeps:  promptDeps,
})
if err != nil {
	return err
}

result, err := workflows.Video.RunAndSave(ctx, recipe, "video_music_meta.json")
```

`VideoGenerationRequest` の主なフィールドは以下の契約で使われます。

| フィールド | adapter 側の扱い |
| --- | --- |
| `Prompt` | Veo に渡す最終プロンプト。カット内容、カメラワーク、音楽同期指示を含みます。 |
| `ImageReference` | 既に参照可能なキーフレーム画像 URI。存在する場合は `InputImage` より優先します。 |
| `InputImage` | `ImageReference` が空の場合に adapter 側でアップロードして使う画像バイト列です。 |
| `AudioReference` | 既に参照可能な音声セグメント URI。 |
| `InputAudio` | `AudioReference` が空の場合に adapter 側でアップロードして使う音声バイト列です。 |
| `PreviousVideoID` | 前カットの文脈を引き継ぐための ID。空の場合はチェーンなしで生成します。 |
| `Seed` | キャラクター・カットの一貫性を保つための seed です。 |
| `CutIndex` | レスポンスやエラー表示で使うカット番号です。 |
| `DurationSec` | カットの目標秒数です。 |

`VideoResponse.VideoID` が空の場合、そのカットの生成結果は保存できますが、次カットへの `PreviousVideoID` 連鎖は更新されません。連続カットの一貫性を重視する adapter では、可能な限り Veo 側の動画 ID を返してください。

---

## 🧾 Music Recipe JSON

`ScriptRunner` はドキュメントから、映像指示だけでなく BGM の拍子・感情・盛り上がりを含む動画台本 JSON を生成します。各 `cut` は `duration_sec` と `audio_cue` を持つため、Veo へのプロンプトには `(synchronized with the heavy bass drop at 0:10)` のような同期指示を自動注入できます。

```json
{
  "project_title": "AIマルチモーダル解説動画",
  "music_recipe": {
    "tempo_bpm": 120,
    "total_duration_sec": 15,
    "style": "90s retro mech synthwave"
  },
  "cuts": [
    {
      "cut_index": 1,
      "duration_sec": 5,
      "audio_cue": "イントロ：静かなシンセのパッド音、秒針の音 (mp3_segment_1)",
      "visual_anchor": "暗闇の中にキャラクターの瞳が光る。カメラがゆっくりと引いていく",
      "character_id": "zundamon"
    },
    {
      "cut_index": 2,
      "duration_sec": 5,
      "audio_cue": "Aメロ：ドラムのビートが刻まれ始める。テンポアップ (mp3_segment_2)",
      "visual_anchor": "ずんだもんが自信満々に人差し指を立てて、カメラに向かって喋る",
      "character_id": "zundamon"
    },
    {
      "cut_index": 3,
      "duration_sec": 5,
      "audio_cue": "サビ：激しいシンセのメロディ、エフェクト音 (mp3_segment_3)",
      "visual_anchor": "カメラが高速で旋回し、背景がサイバー空間へと切り替わる",
      "character_id": "zundamon_metan"
    }
  ]
}
```

この JSON は `Normalize()` により `start_sec` / `end_sec` / `status` が補完されます。生成後は `keyframe_reference`、`video_id`、`video_url` が追記された `video_music_meta.json` として保存されます。

楽曲生成側の JSON が `sections` ベースで届く場合も、そのまま受け付けます。`sections` の要素数は固定せず、各 section の `duration_seconds` から `cuts` を自動生成し、`tempo` / `mood` は `music_recipe.tempo_bpm` / `music_recipe.style` に同期されます。

```json
{
  "title": "碧き残影、一瞬の奇跡 〜黒き疾風の叙事詩〜",
  "theme": "闇を裂き、最速の奇跡を刻む青き瞳の誓い",
  "mood": "Epic Symphonic Fantasy Rock Ballad, Emotional and Melancholic",
  "tempo": 72,
  "instruments": [
    "Acoustic Grand Piano",
    "Soaring Full Strings Section",
    "Progressive Rock Electric Guitar"
  ],
  "sections": [
    {
      "name": "Verse",
      "duration_seconds": 40,
      "prompt": "[Silent Awakening] Focus strictly on the first lyrics block marked [Verse]."
    },
    {
      "name": "Chorus",
      "duration_seconds": 45,
      "prompt": "[Emotional Outburst & High-Voltage Peak] Focus on the lyrics marked [Chorus]."
    }
  ],
  "audio_model": "lyria-3-pro-preview",
  "compose_mode": "game_fantasy",
  "seed": 10
}
```

---

## 📂 プロジェクト構造 (Project Structure)

本アーキテクチャは **ports による抽象化（Hexagonal Architecture）** を境界線としており、Veo API のエンドポイント変更や動画合成エンジンの差し替えを容易に行える設計を採用しています。

```text
go-veo-orchestrator/
├── workflow/    # 【統合管理】各工程を組み合わせ、Workflows インターフェースを実装。
├── runner/      # 【実行実体】Design/Script/CutKeyframe/VideoTimeline/Publish の具体的なプロセス実装。
├── keyframe/    # 【キーフレーム生成戦略】Music Recipe のカット列に基づくキャラクター一貫性つき静止画生成。
└── ports/       # 【契約・定義】Interface（VideoRunner等）、共通モデル、動作設定(Config)。全ての起点。

```

---

## 🔄 シーケンスフロー (Sequence Flow)

### Video Orchestration Flow (`NewVideoTimelineRunner`)

```mermaid
sequenceDiagram
  participant WF as workflow.manager
  participant Composer as keyframe.VideoComposer
  participant KeyframeGen as keyframe.KeyframeGenerator
  participant Timeline as runner.VideoTimelineRunner
  participant Builder as runner.VideoRequestBuilder
  participant VeoAPI as Vertex AI (Veo API)
  participant Writer as remoteio.Writer

  Note over WF,KeyframeGen: 1) GenerationUnit / Keyframe Runner 初期化
  WF->>Composer: keyframe.NewVideoComposer(core, charactersMap)
  Composer-->>WF: *keyframe.VideoComposer
  WF->>KeyframeGen: keyframe.NewKeyframeGenerator(composer, imageGenerator, keyframePrompt, model, opts...)
  KeyframeGen-->>WF: *keyframe.KeyframeGenerator
  WF->>Timeline: runner.NewVideoTimelineRunner(keyframeRunner, videoRunner, publisher)
  Timeline-->>WF: *runner.VideoTimelineRunner

  Note over WF,Timeline: 2) Music Recipeに基づく数珠繋ぎ動画生成
  WF->>Timeline: Run(ctx, recipe) / RunAndSave(ctx, recipe, outputPath)
  Timeline->>KeyframeGen: Execute(ctx, recipe.Cuts)
  KeyframeGen->>Composer: PrepareCharacterResources(ctx, cuts)
  Composer-->>KeyframeGen: Character Base URI (GCS / File API)

  Note over Timeline,VeoAPI: Loop内の Video-to-Video で前カットのコンテキスト(lastVideoID)を連鎖
  Note over Timeline: generated cut は video_id を使ってスキップ可能

  loop cuts / sequential Video-to-Video chain
    Timeline->>Builder: Build(recipe, cut, keyframe, lastVideoID)
    Builder-->>Timeline: VideoGenerationRequest
    Timeline->>VeoAPI: GenerateVideo(Prompt + KeyframeReference/InputImage + AudioReference + PreviousVideoID + Seed)
    VeoAPI-->>Timeline: VideoResponse (CloudURL + VideoID)
    Timeline->>Timeline: cut.video_id / cut.video_url / cut.status 更新
  end

  opt RunAndSave
    Timeline->>Writer: Write(ctx, video_music_meta.json, updatedVideoRecipeJSON, remoteio.WithContentType("application/json"), ...)
    Timeline-->>WF: *ports.VideoPlotResponse
  end

```

---

### 🤝 依存関係 (Dependencies)

* [shouni/gemini-image-kit](https://github.com/shouni/gemini-image-kit) - 静止画・キーフレーム生成コア基盤

### 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
