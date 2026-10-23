package engine

import "testing"

func TestFormatJSON(t *testing.T) {
	for _, c := range []struct{ name, input, want string }{
		{"test_simple_object", `{"foo":"bar","baz":"qux"}`, `{
  "foo": "bar",
  "baz": "qux"
}`},
		{"test_escaped", `{"foo":"Hi \"world!\""}`, `{
  "foo": "Hi \"world!\""
}`},
		{"test_simple_array", `["foo","bar","baz","qux"]`, `[
  "foo",
  "bar",
  "baz",
  "qux"
]`},
		{"test_extra_whitespace", `["foo",   "bar",  "baz","qux"

            ]`, `[
  "foo",
  "bar",
  "baz",
  "qux"
]`},
		{"test_invalid_json", `["foo", {"bar",  }"baz",["qux" ]]`, `[
  "foo",
  {
    "bar",
  }"baz",
  [
    "qux"
  ]
]`},
		{"test_skip_template_tags", `{"foo":${[ fn("hello", "world") ]} }`, `{
  "foo": ${[ fn("hello", "world") ]}
}`},
		{"test_skip_template_tags_with_quoted_brackets", `{"foo":${[ fn(a='x]}y') ]} }`, `{
  "foo": ${[ fn(a='x]}y') ]}
}`},
		{"test_skip_template_tags_with_escaped_quote", `{"foo":${[ fn(a='it\'s ]}') ]} }`, `{
  "foo": ${[ fn(a='it\'s ]}') ]}
}`},
		{"test_graphql_response", `{"data":{"capsules":[{"landings":null,"original_launch":null,"reuse_count":0,"status":"retired","type":"Dragon 1.0","missions":null},{"id":"5e9e2c5bf3591882af3b2665","landings":null,"original_launch":null,"reuse_count":0,"status":"retired","type":"Dragon 1.0","missions":null}]}}`, `{
  "data": {
    "capsules": [
      {
        "landings": null,
        "original_launch": null,
        "reuse_count": 0,
        "status": "retired",
        "type": "Dragon 1.0",
        "missions": null
      },
      {
        "id": "5e9e2c5bf3591882af3b2665",
        "landings": null,
        "original_launch": null,
        "reuse_count": 0,
        "status": "retired",
        "type": "Dragon 1.0",
        "missions": null
      }
    ]
  }
}`},
		{"test_immediate_close", `{"bar":[]}`, `{
  "bar": []
}`},
		{"test_more_closes", `{}}`, `{}
}`},
		{"test_line_comment_between_keys", `{"foo":"bar",// a comment
"baz":"qux"}`, `{
  "foo": "bar", // a comment
  "baz": "qux"
}`},
		{"test_line_comment_at_end", `{"foo":"bar" // trailing
}`, `{
  "foo": "bar" // trailing
}`},
		{"test_block_comment", `{"foo":"bar",/* comment */"baz":"qux"}`, `{
  "foo": "bar",
  /* comment */
  "baz": "qux"
}`},
		{"test_comment_in_array", `[1,// item comment
2,3]`, `[
  1, // item comment
  2,
  3
]`},
		{"test_comment_only_line", `{
  // this is a standalone comment
  "foo": "bar"
}`, `{
  // this is a standalone comment
  "foo": "bar"
}`},
		{"test_multiline_block_comment", `{
  "foo": "bar"
  /**
   Hello World!

   Hi there
   */
}`, `{
  "foo": "bar"
  /**
   Hello World!

   Hi there
   */
}`},
		{"test_comment_inside_string_ignored", `{"foo":"// not a comment","bar":"/* also not */"}`, `{
  "foo": "// not a comment",
  "bar": "/* also not */"
}`},
		{"test_comment_on_line_after_comma", `{
  "a": "aaa",
  // "b": "bbb"
}`, `{
  "a": "aaa",
  // "b": "bbb"
}`},
	} {
		if got := FormatJSON(c.input, "  "); got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

func TestPrettyJSONResponse(t *testing.T) {
	got := PrettyJSONResponse(`{"name":"caf\u00e9","emoji":"\ud83d\ude00","url":"https:\/\/x.dev\/a"}`)
	want := "{\n  \"name\": \"café\",\n  \"emoji\": \"😀\",\n  \"url\": \"https://x.dev/a\"\n}"
	if got != want {
		t.Errorf("%s", got)
	}
}
