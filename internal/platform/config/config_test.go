package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// setRequired sets the mandatory variables to synthetic values. Secrets are
// built at runtime so they never look like real keys to secret scanners.
func setRequired(t *testing.T) {
	t.Setenv("STORE", "memory")
	t.Setenv("JWT_SECRET", strings.Repeat("j", 32))
	t.Setenv("JWT_ISSUER", "test")
	t.Setenv("TWOC2P_MERCHANT_ID", "JT04")
	t.Setenv("TWOC2P_SECRET_KEY", strings.Repeat("k", 32))
}

func TestLoadKafkaDefaultsToDisabled(t *testing.T) {
	setRequired(t)
	t.Setenv("KAFKA_BROKERS", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.KafkaBrokers) != 0 || c.OutboxPollInterval != time.Second {
		t.Fatalf("brokers=%v interval=%s", c.KafkaBrokers, c.OutboxPollInterval)
	}
}

func TestLoadParsesKafkaSettings(t *testing.T) {
	setRequired(t)
	t.Setenv("KAFKA_BROKERS", " kafka-1:9092, ,kafka-2:9092 ")
	t.Setenv("OUTBOX_POLL_INTERVAL", "250ms")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.KafkaBrokers, []string{"kafka-1:9092", "kafka-2:9092"}) || c.OutboxPollInterval != 250*time.Millisecond {
		t.Fatalf("brokers=%v interval=%s", c.KafkaBrokers, c.OutboxPollInterval)
	}
}

func TestLoadRejectsBadPollInterval(t *testing.T) {
	setRequired(t)
	for _, v := range []string{"0s", "-1s", "soon"} {
		t.Setenv("OUTBOX_POLL_INTERVAL", v)
		if _, err := Load(); err == nil {
			t.Errorf("OUTBOX_POLL_INTERVAL=%q: want error", v)
		}
	}
}

func TestLoadDemoUIDefaultsToOff(t *testing.T) {
	setRequired(t)
	t.Setenv("DEMO_UI_ENABLED", "")
	if c, err := Load(); err != nil || c.DemoUI {
		t.Fatalf("DemoUI=%v err=%v", c.DemoUI, err)
	}
	t.Setenv("DEMO_UI_ENABLED", "true")
	if c, err := Load(); err != nil || !c.DemoUI {
		t.Fatalf("DemoUI=%v err=%v", c.DemoUI, err)
	}
	t.Setenv("DEMO_UI_ENABLED", "yes please")
	if _, err := Load(); err == nil {
		t.Fatal("want error for a non-boolean DEMO_UI_ENABLED")
	}
}
