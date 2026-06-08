package keyframe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// negativeKeyframePrompt は単体カットのキーフレームで「文字」や「フキダシ」を排除するための指定です。
const negativeKeyframePrompt = "speech bubble, dialogue balloon, text, alphabet, letters, words, signatures, watermark, username, low quality, distorted, bad anatomy, monochrome, black and white, greyscale"

// KeyframeGenerator は、キャラクターの一貫性を保ちながら並列で複数カットのキーフレームを生成します。
type KeyframeGenerator struct {
	composer       *VideoComposer
	generator      KeyframeImageGenerator
	pb             ports.KeyframePrompt
	model          string
	limiter        *rate.Limiter
	maxConcurrency int
	rateInterval   time.Duration
	rateBurst      int
}

type KeyframeImageGenerator interface {
	GenerateSingleImage(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error)
}

// NewKeyframeGenerator は KeyframeGenerator の新しいインスタンスを初期化します。
func NewKeyframeGenerator(
	composer *VideoComposer,
	generator KeyframeImageGenerator,
	pb ports.KeyframePrompt,
	model string,
	opts ...KeyframeOption,
) *KeyframeGenerator {
	g := &KeyframeGenerator{
		composer:       composer,
		generator:      generator,
		pb:             pb,
		model:          model,
		maxConcurrency: ports.DefaultMaxConcurrency,
		rateInterval:   defaultRateInterval,
		rateBurst:      defaultRateBurst,
	}

	for _, opt := range opts {
		opt(g)
	}

	g.limiter = rate.NewLimiter(rate.Every(g.rateInterval), g.rateBurst)

	return g
}

// Execute は、errgroupの制限機能を使用して同時実行数を制限しながらカットを並列生成します。
func (g *KeyframeGenerator) Execute(ctx context.Context, keyframes []ports.Cut) ([]*imagePorts.ImageResponse, error) {
	if len(keyframes) == 0 {
		return nil, nil
	}

	if err := g.composer.PrepareCharacterResources(ctx, keyframes); err != nil {
		return nil, err
	}

	images := make([]*imagePorts.ImageResponse, len(keyframes))
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(g.maxConcurrency)

	cm := g.composer.Characters

	for i, keyframe := range keyframes {
		eg.Go(func() error {
			if err := g.limiter.Wait(egCtx); err != nil {
				return err
			}

			char := cm.GetCharacterWithDefault(keyframe.CharacterID)
			if char == nil {
				return fmt.Errorf("character not found for character ID '%s'", keyframe.CharacterID)
			}
			seed := char.Seed
			userPrompt, systemPrompt := g.pb.BuildCut(keyframe, char)
			fileURI := g.composer.GetCharacterResourceURI(char.ID)

			logger := slog.With(
				"keyframe_index", i+1,
				"character_id", char.ID,
				"character_name", char.Name,
				"seed", seed,
				"use_file_api", fileURI != "",
			)
			logger.Info("Starting keyframe generation")

			startTime := time.Now()
			resp, err := g.generator.GenerateSingleImage(egCtx, imagePorts.SingleImageRequest{
				GenerationOptions: imagePorts.GenerationOptions{
					Model:          g.model,
					Prompt:         userPrompt,
					SystemPrompt:   systemPrompt,
					NegativePrompt: negativeKeyframePrompt,
					AspectRatio:    CutAspectRatio,
					ImageSize:      ImageSize1K,
					Seed:           seed,
				},
				Image: imagePorts.ImageURI{
					FileAPIURI:   fileURI,
					ReferenceURL: char.ReferenceURL,
				},
			})
			if err != nil {
				return fmt.Errorf("cut %d (character_id: %s) keyframe generation failed: %w", i+1, char.ID, err)
			}

			logger.Info("Keyframe generation completed",
				"duration", time.Since(startTime).Round(time.Second),
			)
			images[i] = resp
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return images, nil
}
