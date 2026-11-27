package jobs_test

import (
	"slices"
	"testing"

	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/media/jobs"
	"wf/backend/internal/platform/queue"
)

// §3.3: media.process — очередь media, 10 попыток, одна живая задача на объект (уникальность по аргументам среди
// available, scheduled, pending, retryable, running; завершённая не мешает). media.purge — очередь media, 25 попыток.
func TestInsertOpts(t *testing.T) {
	t.Parallel()
	p := jobs.ProcessInsertOpts()
	want := []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
		rivertype.JobStateRunning, rivertype.JobStateScheduled}
	got := slices.Clone(p.UniqueOpts.ByState)
	slices.Sort(got)
	if p.Queue != queue.Media || p.MaxAttempts != 10 || !p.UniqueOpts.ByArgs || !slices.Equal(got, want) ||
		p.UniqueOpts.ByPeriod != 0 || p.UniqueOpts.ByQueue {
		t.Fatalf("process: %+v", p)
	}
	if k := (jobs.ProcessArgs{}).Kind(); k != "media.process" {
		t.Fatalf("kind: %s", k)
	}
	g := jobs.PurgeInsertOpts()
	if g.Queue != queue.Media || g.MaxAttempts != 25 {
		t.Fatalf("purge: %+v", g)
	}
	if k := (jobs.PurgeArgs{}).Kind(); k != "media.purge" {
		t.Fatalf("kind: %s", k)
	}
}
