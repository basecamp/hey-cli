package cmd

import "testing"

func TestFormatMessageContent(t *testing.T) {
	tests := []struct {
		name    string
		message string
		rawHTML bool
		want    string
	}{
		{
			name:    "separate paragraphs",
			message: "Hello,\n\nSecond paragraph.\n\nRegards,\nIvan",
			want:    "<p>Hello,</p>\n<p>Second paragraph.</p>\n<p>Regards,<br>Ivan</p>",
		},
		{
			name:    "normalizes Windows newlines",
			message: "First\r\n\r\nSecond\rThird",
			want:    "<p>First</p>\n<p>Second<br>Third</p>",
		},
		{
			name:    "escapes HTML in plain text",
			message: `Use <p> & "quotes"`,
			want:    "<p>Use &lt;p&gt; &amp; &#34;quotes&#34;</p>",
		},
		{
			name:    "preserves explicit HTML",
			message: "<p>Hello</p><ul><li>One</li></ul>",
			rawHTML: true,
			want:    "<p>Hello</p><ul><li>One</li></ul>",
		},
		{
			name:    "trims surrounding whitespace",
			message: "\n\n Hello \n\n",
			want:    "<p>Hello</p>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatMessageContent(tt.message, tt.rawHTML); got != tt.want {
				t.Fatalf("formatMessageContent() = %q, want %q", got, tt.want)
			}
		})
	}
}
