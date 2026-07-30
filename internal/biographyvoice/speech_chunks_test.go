package biographyvoice

import (
	"reflect"
	"testing"
)

func TestStableSpeechChunksWaitsForSentenceAndFlushesTail(t *testing.T) {
	text := "您刚才说到第一次离开家，这段经历很有分量。那时最先看到的是什么"
	chunks, consumed := stableSpeechChunks(text, 0, false)
	if want := []string{"您刚才说到第一次离开家，这段经历很有分量。"}; !reflect.DeepEqual(chunks, want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
	if consumed <= 0 || text[:consumed] != chunks[0] {
		t.Fatalf("unexpected consumed prefix: %d %q", consumed, text[:consumed])
	}

	chunks, finalConsumed := stableSpeechChunks(text, consumed, true)
	if want := []string{"那时最先看到的是什么"}; !reflect.DeepEqual(chunks, want) || finalConsumed != len(text) {
		t.Fatalf("flushed chunks = %#v consumed=%d", chunks, finalConsumed)
	}
}

func TestStableSpeechChunksAllowsLongCommaClause(t *testing.T) {
	text := "您刚才讲到十九岁第一次独自离开家去上海学手艺，后来最难适应的是什么？"
	chunks, consumed := stableSpeechChunks(text, 0, false)
	if want := []string{"您刚才讲到十九岁第一次独自离开家去上海学手艺，", "后来最难适应的是什么？"}; !reflect.DeepEqual(chunks, want) || consumed != len(text) {
		t.Fatalf("chunks = %#v consumed=%d", chunks, consumed)
	}
}

func TestWithBiographySpeechPaceIsIdempotent(t *testing.T) {
	got := withBiographySpeechPace("温和、自然")
	if got != "温和、自然；"+biographySpeechPaceInstruction {
		t.Fatalf("unexpected speech style: %q", got)
	}
	if repeated := withBiographySpeechPace(got); repeated != got {
		t.Fatalf("speech pace duplicated: %q", repeated)
	}
}
