package testutil

import (
	"time"

	"agent-blame/internal/model"
)

// Scenario is the reference story used across tests: PR 41 merged with one
// prompt, PR 42 open with three prompts, and a mix of exact, inferred,
// uncommitted and orphaned edits.
type Scenario struct {
	Repo   *Repo
	Events []model.Event
	T0     time.Time
}

// BuildScenario creates the repository and the matching events.
func BuildScenario(r *Repo) Scenario {
	t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	at := func(min, sec int) time.Time {
		return t0.Add(time.Duration(min)*time.Minute + time.Duration(sec)*time.Second)
	}

	r.Commit(at(0, 0), "initial", map[string]string{
		"README.md":           "app\n",
		"src/auth/session.py": "def expired(s):\n    return s.expires < now()\n",
		"src/routes/auth.py":  "@router.post('/login')\ndef login(req):\n    pass\n",
		"config/settings.py":  "SESSION_TTL = 3600\n",
	})

	r.Git("checkout", "-q", "-b", "session-expiry")
	r.Commit(at(31, 5), "use utc for session ttl", map[string]string{
		"src/auth/session.py": "def expired(s):\n    return s.expires < now(UTC)\n",
	})
	r.Git("checkout", "-q", "main")
	r.Merge(at(62, 40), "session-expiry", "Merge pull request #41 from acme/session-expiry")

	r.Git("checkout", "-q", "-b", "rate-limit")
	r.Commit(at(242, 18), "add login rate limiter", map[string]string{
		"src/middleware/rate_limit.py": "class RateLimiter:\n    pass\n",
		"src/routes/auth.py":           "@router.post('/login')\n@limited(limiter)\ndef login(req):\n    pass\n",
		"config/settings.py":           "SESSION_TTL = 3600\nLOGIN_RATE_LIMIT = 5\n",
	})
	r.Commit(at(248, 30), "reset limiter state in tests", map[string]string{
		"src/middleware/rate_limit.py": "class RateLimiter:\n    def reset(self):\n        pass\n",
		"tests/test_auth.py":           "def test_login():\n    pass\n",
	})
	// Uncommitted work from the third prompt.
	r.Write("config/settings.py", "SESSION_TTL = 3600\nRATE_LIMITS = {'login': 5}\n")

	ok := model.Bool(true)
	fail := model.Bool(false)
	ev := func(id string, ts time.Time, kind model.Kind, branch, parent, subject string) model.Event {
		return model.Event{V: 1, ID: id, TS: ts, Source: model.SourceClaudeCode, Session: "s-" + branch, Kind: kind, Branch: branch, Parent: parent, Subject: subject}
	}
	run := func(id string, ts, end time.Time, branch, parent, cmd string, good *bool, class string) model.Event {
		e := ev(id, ts, model.KindRun, branch, parent, cmd)
		e.End, e.OK, e.RunClass = end, good, class
		return e
	}
	edit := func(id string, ts time.Time, branch, parent, path string, add, del int, why string) model.Event {
		e := ev(id, ts, model.KindEdit, branch, parent, path)
		e.Stat = &model.Stat{Add: add, Del: del}
		e.Hunk = "@@ -1,1 +1,1 @@\n-old\n+new\n"
		if why != "" {
			e.Note = model.Note{Why: why, Origin: model.OriginTranscript}
		}
		return e
	}

	events := []model.Event{
		ev("p41", at(30, 20), model.KindPrompt, "session-expiry", "", "sessions expire too early, fix"),
		ev("r41", at(30, 31), model.KindRead, "session-expiry", "p41", "src/auth/session.py"),
		edit("e41", at(31, 2), "session-expiry", "p41", "src/auth/session.py", 1, 1, "Expiry was compared against local time."),
		run("c41", at(31, 4), at(31, 6), "session-expiry", "p41", "git commit -am 'use utc for session ttl'", ok, "git"),

		ev("p1", at(241, 2), model.KindPrompt, "rate-limit", "", "add rate limiting to /login"),
		ev("r1", at(241, 9), model.KindRead, "rate-limit", "p1", "src/routes/auth.py"),
		edit("e1", at(241, 40), "rate-limit", "p1", "src/middleware/rate_limit.py", 48, 0, "Middleware so other endpoints can reuse it."),
		edit("e2", at(242, 3), "rate-limit", "p1", "src/routes/auth.py", 6, 1, "Only /login, because the prompt scoped it there."),
		edit("e3", at(242, 10), "rate-limit", "p1", "config/settings.py", 3, 0, ""),
		run("c1", at(242, 17), at(242, 19), "rate-limit", "p1", "git add -A && git commit -m 'add login rate limiter'", ok, "git"),

		ev("p2", at(246, 44), model.KindPrompt, "rate-limit", "", "tests are failing, fix it"),
		run("t1", at(246, 50), at(246, 55), "rate-limit", "p2", "pytest -q", fail, "test"),
		edit("e4", at(247, 31), "rate-limit", "p2", "src/middleware/rate_limit.py", 5, 2, "Tests need a clean limiter per case."),
		edit("e5", at(247, 48), "rate-limit", "p2", "tests/test_auth.py", 22, 4, ""),
		run("t2", at(248, 5), at(248, 8), "rate-limit", "p2", "pytest -q", ok, "test"),

		ev("p3", at(255, 20), model.KindPrompt, "rate-limit", "", "make the limit configurable per route"),
		edit("e6", at(255, 41), "rate-limit", "p3", "config/settings.py", 9, 3, "Revises the constants from the first prompt."),
		edit("e7", at(255, 58), "rate-limit", "p3", "src/routes/auth.py", 2, 2, "Passes the route name into the limiter."),
	}
	return Scenario{Repo: r, Events: events, T0: t0}
}
