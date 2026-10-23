package engine

import "testing"

func TestStripJSONComments(t *testing.T) {
	for _, c := range []struct{ name, input, want string }{
		{"test_no_comments", `{
  "foo": "bar",
  "baz": 123
}`, `{
  "foo": "bar",
  "baz": 123
}`},
		{"test_trailing_line_comment", `{
  "foo": "bar", // this is a comment
  "baz": 123
}`, `{
  "foo": "bar",
  "baz": 123
}`},
		{"test_whole_line_comment", `{
  // this is a comment
  "foo": "bar"
}`, `{
  "foo": "bar"
}`},
		{"test_inline_block_comment", `{
  "foo": /* a comment */ "bar"
}`, `{
  "foo": "bar"
}`},
		{"test_whole_line_block_comment", `{
  /* a comment */
  "foo": "bar"
}`, `{
  "foo": "bar"
}`},
		{"test_multiline_block_comment", `{
  /**
   * Hello World!
   */
  "foo": "bar"
}`, `{
  "foo": "bar"
}`},
		{"test_comment_inside_string_preserved", `{
  "foo": "// not a comment",
  "bar": "/* also not */"
}`, `{
  "foo": "// not a comment",
  "bar": "/* also not */"
}`},
		{"test_comment_inside_template_tag_preserved", `{
  "foo": ${[ fn("// hi", "/* hey */") ]}
}`, `{
  "foo": ${[ fn("// hi", "/* hey */") ]}
}`},
		{"test_quoted_tag_close_inside_template_tag", `{
  "foo": ${[ fn(a='x]}y // not a comment') ]}
}`, `{
  "foo": ${[ fn(a='x]}y // not a comment') ]}
}`},
		{"test_escaped_quote_inside_template_tag", `{
  "foo": ${[ fn(a='it\'s ]} // still fine') ]}
}`, `{
  "foo": ${[ fn(a='it\'s ]} // still fine') ]}
}`},
		{"test_unterminated_quote_closes_tag_at_first_bracket", `{"foo": ${[ fn(a='oops ]}, "bar": 1 // gone
}`, `{"foo": ${[ fn(a='oops ]}, "bar": 1
}`},
		{"test_multiple_comments", `{
  // first comment
  "foo": "bar", // trailing
  /* block */
  "baz": 123
}`, `{
  "foo": "bar",
  "baz": 123
}`},
		{"test_trailing_comma_after_comment_removed", `{
  "a": "aaa",
  // "b": "bbb"
}`, `{
  "a": "aaa"
}`},
		{"test_trailing_comma_in_array", `[1, 2, /* 3 */]`, `[1, 2]`},
		{"test_comma_inside_string_preserved", `{"a": "hello,}`, `{"a": "hello,}`},
	} {
		if got := StripJSONComments(c.input); got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

func TestFixJSONBodyStripsOnlyToValidJSON(t *testing.T) {
	if got := FixJSONBody("{\r\n  \"a\": 1, // one\r\n}"); got != "{\n  \"a\": 1\n}" {
		t.Errorf("%q", got)
	}
	if got := FixJSONBody("{ // open\n"); got != "{ // open\n" {
		t.Errorf("%q", got)
	}
}
