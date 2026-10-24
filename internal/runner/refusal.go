package runner

import (
	"context"
	"fmt"
	"time"

	"bsv-docflow/internal/agentio"
)

func refusalLabel(kind string) string {
	switch kind {
	case agentio.RefusalLimit:
		return "usage limit"
	case agentio.RefusalOutage:
		return "provider unavailable"
	case agentio.RefusalThrottle:
		return "throttled"
	default:
		return "provider refusal"
	}
}

func retryRefusalKind(kind string, consecutiveThrottles int) (string, int) {
	if kind != agentio.RefusalThrottle {
		return kind, 0
	}
	consecutiveThrottles++
	if consecutiveThrottles >= 4 {
		return agentio.RefusalOutage, 0
	}
	return kind, consecutiveThrottles
}

func refusalSchedule(agent map[string]any, refusal *agentio.Refusal, now time.Time) (string, time.Time, error) {
	var until time.Time
	var message string
	switch refusal.Kind {
	case agentio.RefusalLimit:
		seconds := agentio.LimitUntil(agent, refusal.Text, float64(now.Unix()), refusal.Reset)
		until = time.Unix(int64(seconds), 0)
		message = "usage limit until " + until.Local().Format("15:04")
	case agentio.RefusalOutage:
		until = now.Add(10 * time.Minute)
		message = "provider unavailable, retry at " + until.Local().Format("15:04")
	case agentio.RefusalThrottle:
		until = now.Add(30 * time.Second)
		message = "throttled, retry in 30 s"
	default:
		return "", time.Time{}, fmt.Errorf("unknown provider refusal kind %q", refusal.Kind)
	}
	return message, until, nil
}

func waitRefusal(ctx context.Context, agent map[string]any, refusal *agentio.Refusal, progress agentio.LimitProgress) error {
	message, until, err := refusalSchedule(agent, refusal, time.Now())
	if err != nil {
		return err
	}
	progress.Line(message, true)
	progress.Spin(message)
	defer progress.Stop()
	timer := time.NewTimer(time.Until(until))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
