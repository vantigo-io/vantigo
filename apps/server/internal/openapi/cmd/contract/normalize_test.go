package main

import (
	"reflect"
	"testing"
)

func TestNormalizeNumbers(t *testing.T) {
	cases := []struct {
		name  string
		in    obj
		want  obj
		count int
	}{
		{
			name:  "int32 typed and pattern removed",
			in:    obj{"format": "int32", "pattern": `^-?(?:0|[1-9]\d*)$`},
			want:  obj{"format": "int32", "type": "integer"},
			count: 1,
		},
		{
			name:  "int64 typed and pattern removed",
			in:    obj{"format": "int64", "pattern": `^-?(?:0|[1-9]\d*)$`},
			want:  obj{"format": "int64", "type": "integer"},
			count: 1,
		},
		{
			name:  "double typed and pattern removed",
			in:    obj{"format": "double", "pattern": `^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`},
			want:  obj{"format": "double", "type": "number"},
			count: 1,
		},
		{
			name:  "float typed and pattern removed",
			in:    obj{"format": "float", "pattern": `^-?(?:0|[1-9]\d*)(?:\.\d+)?$`},
			want:  obj{"format": "float", "type": "number"},
			count: 1,
		},
		{
			name:  "nullable kept",
			in:    obj{"format": "int64", "nullable": true, "pattern": `^-?(?:0|[1-9]\d*)$`},
			want:  obj{"format": "int64", "nullable": true, "type": "integer"},
			count: 1,
		},
		{
			name:  "typed integer loses its numeric-string pattern",
			in:    obj{"type": "integer", "format": "int32", "pattern": `^-?(?:0|[1-9]\d*)$`},
			want:  obj{"type": "integer", "format": "int32"},
			count: 1,
		},
		{
			name:  "typed number loses its numeric-string pattern",
			in:    obj{"type": "number", "format": "double", "pattern": `^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`},
			want:  obj{"type": "number", "format": "double"},
			count: 1,
		},
		{
			name:  "string keeps a numeric-string pattern",
			in:    obj{"type": "string", "pattern": `^-?(?:0|[1-9]\d*)$`},
			want:  obj{"type": "string", "pattern": `^-?(?:0|[1-9]\d*)$`},
			count: 0,
		},
		{
			name:  "already-typed schema untouched",
			in:    obj{"type": "string", "format": "date-time"},
			want:  obj{"type": "string", "format": "date-time"},
			count: 0,
		},
		{
			name:  "string schema with a non-numeric pattern untouched",
			in:    obj{"format": "email", "pattern": `^[^@]+@[^@]+$`},
			want:  obj{"format": "email", "pattern": `^[^@]+@[^@]+$`},
			count: 0,
		},
		{
			name: "nested property inside items/properties/allOf reached",
			in: obj{
				"type": "object",
				"properties": obj{
					"id": obj{"format": "int64", "pattern": `^-?(?:0|[1-9]\d*)$`},
				},
				"items": obj{"format": "int32"},
				"allOf": []any{
					obj{"format": "double", "pattern": `^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`},
				},
			},
			want: obj{
				"type": "object",
				"properties": obj{
					"id": obj{"format": "int64", "type": "integer"},
				},
				"items": obj{"format": "int32", "type": "integer"},
				"allOf": []any{
					obj{"format": "double", "type": "number"},
				},
			},
			count: 3,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeNumbers(c.in)
			if got != c.count {
				t.Errorf("count = %d, want %d", got, c.count)
			}
			if !reflect.DeepEqual(c.in, c.want) {
				t.Errorf("got %#v, want %#v", c.in, c.want)
			}
		})
	}
}

func TestNormalizeNullableRefs(t *testing.T) {
	cases := []struct {
		name  string
		in    obj
		want  obj
		count int
	}{
		{
			name: "nullable single-$ref oneOf becomes allOf",
			in: obj{
				"nullable": true,
				"oneOf":    []any{obj{"$ref": "#/components/schemas/AddressRequest"}},
				"type":     "object",
			},
			want: obj{
				"nullable": true,
				"allOf":    []any{obj{"$ref": "#/components/schemas/AddressRequest"}},
			},
			count: 1,
		},
		{
			name: "description survives the rewrite",
			in: obj{
				"nullable":    true,
				"description": "the ended period, if any",
				"oneOf":       []any{obj{"$ref": "#/components/schemas/SupplyPeriodResponse"}},
				"type":        "object",
			},
			want: obj{
				"nullable":    true,
				"description": "the ended period, if any",
				"allOf":       []any{obj{"$ref": "#/components/schemas/SupplyPeriodResponse"}},
			},
			count: 1,
		},
		{
			name: "multi-element oneOf left alone",
			in: obj{
				"nullable": true,
				"oneOf": []any{
					obj{"$ref": "#/components/schemas/A"},
					obj{"$ref": "#/components/schemas/B"},
				},
			},
			want: obj{
				"nullable": true,
				"oneOf": []any{
					obj{"$ref": "#/components/schemas/A"},
					obj{"$ref": "#/components/schemas/B"},
				},
			},
			count: 0,
		},
		{
			name: "non-nullable single oneOf left alone",
			in: obj{
				"oneOf": []any{obj{"$ref": "#/components/schemas/A"}},
				"type":  "object",
			},
			want: obj{
				"oneOf": []any{obj{"$ref": "#/components/schemas/A"}},
				"type":  "object",
			},
			count: 0,
		},
		{
			name: "single oneOf element with extra keys left alone",
			in: obj{
				"nullable": true,
				"oneOf":    []any{obj{"$ref": "#/components/schemas/A", "description": "x"}},
			},
			want: obj{
				"nullable": true,
				"oneOf":    []any{obj{"$ref": "#/components/schemas/A", "description": "x"}},
			},
			count: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeNullableRefs(c.in)
			if got != c.count {
				t.Errorf("count = %d, want %d", got, c.count)
			}
			if !reflect.DeepEqual(c.in, c.want) {
				t.Errorf("got %#v, want %#v", c.in, c.want)
			}
		})
	}
}
