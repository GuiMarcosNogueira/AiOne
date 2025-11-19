package mock

import (
	"context"
	"strings"
	"testing"

	"github.com/midia/aione/internal/providers/dto"
)

func TestMockProviderImplementsAllMethods(t *testing.T) {
	provider := New("mock-provider")
	if provider.Name() != "mock-provider" {
		t.Fatalf("expected provider name")
	}

	caps := provider.Capabilities()
	if !caps.TextGeneration || !caps.ImageGeneration || !caps.SpeechToText {
		t.Fatalf("expected capabilities to be enabled: %+v", caps)
	}

	if err := provider.Health(context.TODO()); err != nil {
		t.Fatalf("health should succeed: %v", err)
	}
	if err := provider.Health(context.TODO()); err != nil {
		t.Fatalf("health should succeed: %v", err)
	}

	text, err := provider.TextGenerate(context.TODO(), dto.TextReq{Prompt: "hello"})
	if err != nil || !strings.Contains(text.Content, "hello") {
		t.Fatalf("text generation failed: %+v %v", text, err)
	}
	text, err = provider.TextGenerate(context.TODO(), dto.TextReq{Prompt: "hello"})
	if err != nil || !strings.Contains(text.Content, "hello") {
		t.Fatalf("text generation failed: %+v %v", text, err)
	}

	image, err := provider.ImageGenerate(context.TODO(), dto.ImageReq{Prompt: "sunrise"})
	if err != nil || !strings.Contains(image.URL, "mock-provider") {
		t.Fatalf("image generation failed: %+v %v", image, err)
	}
	image, err = provider.ImageGenerate(context.TODO(), dto.ImageReq{Prompt: "sunrise"})
	if err != nil || !strings.Contains(image.URL, "mock-provider") {
		t.Fatalf("image generation failed: %+v %v", image, err)
	}

	video, err := provider.VideoGenerate(context.TODO(), dto.VideoReq{Prompt: "clip"})
	if err != nil || !strings.Contains(video.URL, "mock-provider") {
		t.Fatalf("video generation failed: %+v %v", video, err)
	}
	video, err = provider.VideoGenerate(context.TODO(), dto.VideoReq{Prompt: "clip"})
	if err != nil || !strings.Contains(video.URL, "mock-provider") {
		t.Fatalf("video generation failed: %+v %v", video, err)
	}

	stt, err := provider.SpeechToText(context.TODO(), dto.STTReq{AudioURL: "http://audio"})
	if err != nil || !strings.Contains(stt.Transcript, "http://audio") {
		t.Fatalf("speech to text failed: %+v %v", stt, err)
	}
	stt, err = provider.SpeechToText(context.TODO(), dto.STTReq{AudioURL: "http://audio"})
	if err != nil || !strings.Contains(stt.Transcript, "http://audio") {
		t.Fatalf("speech to text failed: %+v %v", stt, err)
	}

	tts, err := provider.TextToSpeech(context.TODO(), dto.TTSReq{Text: "speak"})
	if err != nil || !strings.Contains(tts.AudioURL, "mock-provider") {
		t.Fatalf("text to speech failed: %+v %v", tts, err)
	}
	tts, err = provider.TextToSpeech(context.TODO(), dto.TTSReq{Text: "speak"})
	if err != nil || !strings.Contains(tts.AudioURL, "mock-provider") {
		t.Fatalf("text to speech failed: %+v %v", tts, err)
	}

	emb, err := provider.Embeddings(context.TODO(), dto.EmbeddingsReq{Inputs: []string{"abc"}})
	if err != nil || len(emb.Vectors) != 1 || len(emb.Vectors[0]) != 1 {
		t.Fatalf("embeddings failed: %+v %v", emb, err)
	}
	emb, err = provider.Embeddings(context.TODO(), dto.EmbeddingsReq{Inputs: []string{"abc"}})
	if err != nil || len(emb.Vectors) != 1 || len(emb.Vectors[0]) != 1 {
		t.Fatalf("embeddings failed: %+v %v", emb, err)
	}

	mod, err := provider.Moderation(context.TODO(), dto.ModerationReq{Input: "text"})
	if err != nil || mod.Flagged {
		t.Fatalf("moderation failed: %+v %v", mod, err)
	}
	mod, err = provider.Moderation(context.TODO(), dto.ModerationReq{Input: "text"})
	if err != nil || mod.Flagged {
		t.Fatalf("moderation failed: %+v %v", mod, err)
	}
}
