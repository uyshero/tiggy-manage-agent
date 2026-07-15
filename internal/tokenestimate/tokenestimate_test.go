package tokenestimate

import "testing"

func TestTextEstimatesASCIIAndUnicodeSeparately(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int
	}{
		{value: "", want: 0},
		{value: "abcdefgh", want: 2},
		{value: "上下文预算", want: 5},
		{value: "test中文", want: 3},
		{value: "A中B", want: 3},
		{value: "🙂🙂", want: 2},
	} {
		if got := Text(test.value); got != test.want {
			t.Fatalf("Text(%q)=%d, want %d", test.value, got, test.want)
		}
	}
}
