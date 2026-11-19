package identity

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// brandMeter names the meter of brand counters (brands design B39).
const brandMeter = "github.com/vantigo-io/vantigo/server/internal/identity/brands"

// brandMetrics are the counters brands keep: the public look's requests, the
// stylesheet's requests by whether they were rendered or answered 304, publishes
// and the environment import's outcomes.
type brandMetrics struct {
	publicLookups metric.Int64Counter
	cssRequests   metric.Int64Counter
	publishes     metric.Int64Counter
	imports       metric.Int64Counter
	ruleChanges   metric.Int64Counter
	choices       metric.Int64Counter
	entityReads   metric.Int64Counter
}

// newBrandMetrics builds the counters on mp, or on the global provider.
func newBrandMetrics(mp metric.MeterProvider) *brandMetrics {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	m := mp.Meter(brandMeter)
	counter := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = noop.NewMeterProvider().Meter(brandMeter).Int64Counter(name)
		}
		return c
	}
	return &brandMetrics{
		publicLookups: counter("vantigo.identity.brand.public_lookups", "Requests for the public brand."),
		cssRequests:   counter("vantigo.identity.brand.css_requests", "Requests for the brand stylesheet, by result: rendered or not_modified."),
		publishes:     counter("vantigo.identity.brand.publishes", "Brand publishes."),
		imports:       counter("vantigo.identity.brand.imports", "The default brand's import of the environment's branding, by outcome: started, completed, logo_failed."),
		ruleChanges:   counter("vantigo.identity.brand.rule_changes", "Replacements of the brand assignment rules."),
		choices:       counter("vantigo.identity.brand.choices", "A person's own choice of brand, by result: chosen or cleared."),
		entityReads:   counter("vantigo.identity.brand.entity_reads", "Reads of the legal entities a person works under, by result: cache or staff."),
	}
}

func cssResultAttr(result string) metric.AddOption {
	return metric.WithAttributes(attribute.String("result", result))
}

func importOutcomeAttr(outcome string) metric.AddOption {
	return metric.WithAttributes(attribute.String("outcome", outcome))
}

func resultAttr(result string) metric.AddOption {
	return metric.WithAttributes(attribute.String("result", result))
}
