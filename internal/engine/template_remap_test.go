package engine

import "testing"

func TestRemapTemplateText(t *testing.T) {
	ids := map[string]string{"req_old": "rq_new"}
	for _, c := range []struct{ text, want string }{
		{"${[ response.header(request='req_old', header='req_old') ]}", "${[ response.header(request='rq_new', header='req_old') ]}"},
		{"req_old", "req_old"},
		{"${[ custom(request='req_old') ]}", "${[ custom(request='req_old') ]}"},
		{"${[ response.header(request=req_old) ]}", "${[ response.header(request=req_old) ]}"},
		{"${[ response.header(request='req_missing') ]}", "${[ response.header(request='req_missing') ]}"},
		{"${[ response.header(request='req_old' ]}", "${[ response.header(request='req_old' ]}"},
		{`café \${[ response.header(request='req_old') ]} ${[ json.jsonpath(input=response.body.raw(request=b64'cmVxX29sZA')) ]} end`,
			`café \${[ response.header(request='req_old') ]} ${[ json.jsonpath(input=response.body.raw(request='rq_new')) ]} end`},
		{"${[ response.header(request='req_old', header='X-Api-Key') ]}", "${[ response.header(request='rq_new', header='X-Api-Key') ]}"},
		{"${[ response.body.path(request='req_old', path='$.token', behavior='never') ]}", "${[ response.body.path(request='rq_new', path='$.token', behavior='never') ]}"},
		{"{% response 'raw', 'req_old', '', 'never', 60 %}", "{% response 'raw', 'req_old', '', 'never', 60 %}"},
		{"${[ response.body.path(request='req_old', path='$.a') ]} and {% response 'raw', 'req_old' %}", "${[ response.body.path(request='rq_new', path='$.a') ]} and {% response 'raw', 'req_old' %}"},
		{"${[ request.header(requestId='req_old', header='A') ]}", "${[ request.header(requestId='rq_new', header='A') ]}"},
	} {
		if got := remapTemplateText(c.text, ids); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.text, got, c.want)
		}
	}
	text := "${[ response.header(request='req_old', header=']}' ) ]}"
	if got := remapTemplateText(text, ids); got != "${[ response.header(request='rq_new', header=']}') ]}" {
		t.Errorf("quoted ]}: %s", got)
	}
}
