package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// CronTool lets agents manage Gateway cron jobs.
// Matching OpenClaw src/agents/tools/cron-tool.ts.
type CronTool struct {
	cronStore store.CronStore
	permStore store.ConfigPermissionStore // nil = no group restriction
}

func NewCronTool(cronStore store.CronStore) *CronTool {
	return &CronTool{cronStore: cronStore}
}

// SetConfigPermStore enables group cron mutation restriction.
func (t *CronTool) SetConfigPermStore(s store.ConfigPermissionStore) {
	t.permStore = s
}

func (t *CronTool) Name() string { return "cron" }

func (t *CronTool) Description() string {
	return `Manage Gateway cron jobs.
Always send a JSON object with an "action" field.

VALID ACTIONS AND EXACT PAYLOAD SHAPES:
1) status
{ "action": "status" }

2) list
{ "action": "list", "includeDisabled": true|false }

3) add
{
  "action": "add",
  "job": {
    "name": "string",             // required, lowercase slug: [a-z0-9-]+
    "schedule": { ... },          // required
    "message": "string",          // required
    "deliver": true|false,        // optional, default false
    "channel": "string",          // optional, auto-filled from current channel context
    "to": "string",               // optional
    "agentId": "string",          // optional, defaults to current agent
    "deleteAfterRun": true|false  // optional, default true for schedule.kind="at"
  }
}

4) update
{
  "action": "update",
  "jobId": "string",
  "patch": {
    "name": "string",
    "schedule": { ... },
    "message": "string",
    "deliver": true|false,
    "channel": "string",
    "to": "string",
    "agentId": "string",
    "deleteAfterRun": true|false,
    "disabled": true|false
  }
}

5) remove
{ "action": "remove", "jobId": "string" }

6) run
{ "action": "run", "jobId": "string" }

7) runs
{ "action": "runs", "jobId": "string" }

SCHEDULE SCHEMA:
- at: { "kind": "at", "atLocal": "<YYYY-MM-DDTHH:mm:ss>", "tz": "<IANA timezone, e.g. Asia/Saigon>" }
- at trusted timestamp fallback: { "kind": "at", "atMs": <unix-milliseconds>, "trustedAtMs": true, "tz": "<IANA timezone, e.g. Asia/Saigon>" }
- every: { "kind": "every", "everyMs": <interval-ms> }
- cron: { "kind": "cron", "expr": "<5-field cron>", "tz": "<IANA timezone, e.g. Asia/Saigon; omit for gateway default>" }

RULES:
- For action="add", send the job inside "job". Do not place job fields at the root level.
- For action="update", send changes inside "patch". Do not place patch fields at the root level.
- Always use "jobId". Do not use "id".
- "name", "schedule", and "message" are required for add.
- "name" must match: lowercase letters, numbers, hyphens only.
- Before creating or updating a scheduled job, call the datetime tool first to get precise current local time in Asia/Saigon unless the user explicitly requested another timezone.
- For natural-language one-shot reminders, use atLocal+tz. Let the LLM interpret the user's language, then pass the resolved local wall-clock time to this tool.
- Use atMs only for non-LLM/programmatic callers that already have a trusted Unix timestamp, and set trustedAtMs=true. If you are resolving natural language, never use atMs.
- Use cron schedules only for recurring jobs. Do not use cron for one-shot reminders unless the user explicitly asks for recurrence.
- Omit optional fields when unknown; do not invent placeholder values like "", 0, or null unless required.
- Jobs run as isolated agent turns using the provided "message".`
}

func (t *CronTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "The cron action to perform",
				"enum":        []string{"status", "list", "add", "update", "remove", "run", "runs"},
			},
			"includeDisabled": map[string]any{
				"type":        "boolean",
				"description": "Include disabled jobs in list (default false)",
			},
			"job": map[string]any{
				"type":                 "object",
				"description":          "Job definition for add action (name, schedule, message, deliver, channel, to, agentId, deleteAfterRun)",
				"additionalProperties": true,
			},
			"jobId": map[string]any{
				"type":        "string",
				"description": "Job ID for update/remove/run/runs actions",
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Backward compatibility alias for jobId",
			},
			"patch": map[string]any{
				"type":                 "object",
				"description":          "Patch object for update action",
				"additionalProperties": true,
			},
			"runMode": map[string]any{
				"type":        "string",
				"description": "Run mode: 'due' (only if due) or 'force' (immediate)",
				"enum":        []string{"due", "force"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *CronTool) Execute(ctx context.Context, args map[string]any) *Result {
	action, _ := args["action"].(string)
	if action == "" {
		return ErrorResult("action parameter is required")
	}

	// Group cron permission check for mutation actions
	if t.permStore != nil && (action == "add" || action == "update" || action == "remove") {
		if err := store.CheckCronPermission(ctx, t.permStore); err != nil {
			return ErrorResult("permission denied: only users with cron or file_writer permission can manage cron jobs in group chats")
		}
	}

	agentID := resolveAgentIDString(ctx)
	// SCOPE-intentional (#915 audit 2026-04-16): cron jobs follow the per-group
	// memory model — all group members share the same cron_jobs rows via the
	// group-scope user_id. Migrating to ActorIDFromContext would split jobs
	// per-individual and break collaborative /cron list/add/remove UX.
	userID := store.UserIDFromContext(ctx)

	switch action {
	case "status":
		return t.handleStatus()
	case "list":
		return t.handleList(ctx, args, agentID, userID)
	case "add":
		return t.handleAdd(ctx, args, agentID, userID)
	case "update":
		return t.handleUpdate(ctx, args, agentID, userID)
	case "remove":
		return t.handleRemove(ctx, args, agentID, userID)
	case "run":
		return t.handleRun(ctx, args, agentID, userID)
	case "runs":
		return t.handleRuns(ctx, args, agentID, userID)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

func (t *CronTool) handleStatus() *Result {
	status := t.cronStore.Status()
	data, _ := json.MarshalIndent(status, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleList(ctx context.Context, args map[string]any, agentID, userID string) *Result {
	includeDisabled, _ := args["includeDisabled"].(bool)
	jobs := t.cronStore.ListJobs(ctx, includeDisabled, agentID, userID)

	result := map[string]any{
		"jobs":  jobs,
		"count": len(jobs),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleAdd(ctx context.Context, args map[string]any, agentID, userID string) *Result {
	jobObj, ok := args["job"].(map[string]any)
	if !ok {
		return ErrorResult("job object is required for add action")
	}

	name, _ := jobObj["name"].(string)
	if name == "" {
		return ErrorResult("job.name is required")
	}

	scheduleObj, ok := jobObj["schedule"].(map[string]any)
	if !ok {
		return ErrorResult("job.schedule is required")
	}

	message, _ := jobObj["message"].(string)
	if message == "" {
		return ErrorResult("job.message is required")
	}

	// Parse schedule
	schedule := store.CronSchedule{
		Kind: stringFromMap(scheduleObj, "kind"),
	}
	if schedule.Kind == "" {
		return ErrorResult("job.schedule.kind is required (at, every, or cron)")
	}

	switch schedule.Kind {
	case "at":
		schedule.TZ = normalizeCronTimezone(stringFromMap(scheduleObj, "tz"))
		if schedule.TZ == "" {
			schedule.TZ = "Asia/Saigon"
		}
		loc, err := time.LoadLocation(schedule.TZ)
		if err != nil {
			return ErrorResult(fmt.Sprintf("invalid timezone '%s': use IANA names like 'Asia/Saigon', 'America/New_York'", schedule.TZ))
		}
		if atLocal := stringFromMap(scheduleObj, "atLocal"); atLocal != "" {
			ms, err := parseAtLocalMS(atLocal, loc)
			if err != nil {
				return ErrorResult(fmt.Sprintf("job.schedule.atLocal is invalid: %v. Use format YYYY-MM-DDTHH:mm:ss, for example 2026-05-02T13:00:00", err))
			}
			if ms <= time.Now().UnixMilli() {
				return ErrorResult(fmt.Sprintf("job.schedule.atLocal is in the past (%s %s). Use a future local time", atLocal, schedule.TZ))
			}
			schedule.AtMS = &ms
		} else if v, ok := numberFromMap(scheduleObj, "atMs"); ok {
			trustedAtMS, _ := scheduleObj["trustedAtMs"].(bool)
			if !trustedAtMS {
				return ErrorResult("job.schedule.atLocal and job.schedule.tz are required for one-shot reminders. Do not use atMs for natural-language scheduling; resolve the user's requested wall-clock time and retry with atLocal like 2026-05-02T14:00:00 plus tz Asia/Saigon.")
			}
			ms := int64(v)
			now := time.Now().UnixMilli()
			if ms <= now {
				if adjusted, ok := adjustNearPastAtMS(ms, now); ok {
					ms = adjusted
				} else {
					return ErrorResult(fmt.Sprintf("job.schedule.atMs is in the past (%d). Use a future Unix timestamp in milliseconds. Current time is %d ms", ms, now))
				}
			}
			schedule.AtMS = &ms
		} else {
			return ErrorResult("job.schedule.atLocal or job.schedule.atMs is required for 'at' schedule")
		}
	case "every":
		if v, ok := numberFromMap(scheduleObj, "everyMs"); ok {
			ms := int64(v)
			schedule.EveryMS = &ms
		} else {
			return ErrorResult("job.schedule.everyMs is required for 'every' schedule")
		}
	case "cron":
		schedule.Expr = stringFromMap(scheduleObj, "expr")
		if schedule.Expr == "" {
			return ErrorResult("job.schedule.expr is required for 'cron' schedule")
		}
		schedule.TZ = normalizeCronTimezone(stringFromMap(scheduleObj, "tz"))
		if schedule.TZ != "" {
			if _, err := time.LoadLocation(schedule.TZ); err != nil {
				return ErrorResult(fmt.Sprintf("invalid timezone '%s': use IANA names like 'Asia/Saigon', 'America/New_York'", schedule.TZ))
			}
		}
	default:
		return ErrorResult(fmt.Sprintf("invalid schedule kind: %s (must be at, every, or cron)", schedule.Kind))
	}

	// Optional fields
	deliver, _ := jobObj["deliver"].(bool)
	channel, _ := jobObj["channel"].(string)
	to, _ := jobObj["to"].(string)

	// Auto-default deliver=true when the request comes from a real channel
	// (not CLI/system/subagent). Users chatting on Zalo/Telegram expect
	// cron results delivered back to the same chat.
	if !deliver {
		if ctxChannel := ToolChannelFromCtx(ctx); ctxChannel != "" {
			if isInternalCronChannel(ctxChannel) {
				// internal channels — don't auto-deliver
			} else {
				deliver = true
			}
		}
	}

	// Auto-fill channel and to from context when deliver is requested.
	// Always prefer context values over LLM-provided values to prevent
	// misrouted deliveries (e.g. LLM confusing guild ID with channel ID).
	if deliver {
		if ctxChannel := ToolChannelFromCtx(ctx); ctxChannel != "" {
			channel = ctxChannel
		}
		if ctxChatID := ToolChatIDFromCtx(ctx); ctxChatID != "" {
			to = ctxChatID
		}
	}

	// Use agent ID from job object if explicitly provided, otherwise from context
	if explicit, _ := jobObj["agentId"].(string); explicit != "" {
		agentID = explicit
	}
	job, err := t.cronStore.AddJob(ctx, name, schedule, message, deliver, channel, to, agentID, userID)
	if err != nil && isDuplicateCronJobNameError(err) {
		name = uniqueCronJobName(name, time.Now())
		job, err = t.cronStore.AddJob(ctx, name, schedule, message, deliver, channel, to, agentID, userID)
	}
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create cron job: %v", err))
	}

	if replyTo := ToolReplyToMessageIDFromCtx(ctx); replyTo != "" {
		payload := job.Payload
		payload.ReplyToMessageID = replyTo
		if threadID := ToolMessageThreadIDFromCtx(ctx); threadID != "" {
			payload.MessageThreadID = threadID
		}
		if updated, uErr := t.cronStore.UpdateJob(ctx, job.ID, store.CronJobPatch{Payload: &payload}); uErr == nil {
			job = updated
		}
	}

	// Set wake_heartbeat if requested (triggers heartbeat after cron job completes)
	if wh, _ := jobObj["wake_heartbeat"].(bool); wh {
		wakeTrue := true
		if updated, uErr := t.cronStore.UpdateJob(ctx, job.ID, store.CronJobPatch{WakeHeartbeat: &wakeTrue}); uErr == nil {
			job = updated
		}
	}

	data, _ := json.MarshalIndent(map[string]any{"job": job}, "", "  ")
	return NewResult(string(data))
}

// checkJobOwnership validates that the job belongs to the current agent+user scope.
// When agentID/userID is empty, all jobs are accessible.
func (t *CronTool) checkJobOwnership(ctx context.Context, jobID, agentID, userID string) (*store.CronJob, *Result) {
	job, ok := t.cronStore.GetJob(ctx, jobID)
	if !ok {
		return nil, ErrorResult(fmt.Sprintf("job %s not found", jobID))
	}

	// Verify ownership
	if agentID != "" && job.AgentID != agentID {
		return nil, ErrorResult(fmt.Sprintf("job %s not found", jobID))
	}
	if userID != "" && job.UserID != userID {
		return nil, ErrorResult(fmt.Sprintf("job %s not found", jobID))
	}

	return job, nil
}

func (t *CronTool) handleUpdate(ctx context.Context, args map[string]any, agentID, userID string) *Result {
	jobID := resolveJobID(args)
	if jobID == "" {
		return ErrorResult("jobId is required for update action")
	}

	if _, errResult := t.checkJobOwnership(ctx, jobID, agentID, userID); errResult != nil {
		return errResult
	}

	patchObj, ok := args["patch"].(map[string]any)
	if !ok {
		return ErrorResult("patch object is required for update action")
	}

	var patch store.CronJobPatch
	// Re-marshal and unmarshal to leverage JSON tags
	patchJSON, _ := json.Marshal(patchObj)
	json.Unmarshal(patchJSON, &patch)

	// Validate atMs not in the past when updating schedule
	if patch.Schedule != nil && patch.Schedule.Kind == "at" && patch.Schedule.AtMS != nil {
		if *patch.Schedule.AtMS <= time.Now().UnixMilli() {
			return ErrorResult(fmt.Sprintf("schedule.atMs is in the past (%d). Use the datetime tool to get current time, then set a future timestamp. Current time is %d ms", *patch.Schedule.AtMS, time.Now().UnixMilli()))
		}
	}

	job, err := t.cronStore.UpdateJob(ctx, jobID, patch)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to update cron job: %v", err))
	}

	data, _ := json.MarshalIndent(map[string]any{"job": job}, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleRemove(ctx context.Context, args map[string]any, agentID, userID string) *Result {
	jobID := resolveJobID(args)
	if jobID == "" {
		return ErrorResult("jobId is required for remove action")
	}

	if _, errResult := t.checkJobOwnership(ctx, jobID, agentID, userID); errResult != nil {
		return errResult
	}

	if err := t.cronStore.RemoveJob(ctx, jobID); err != nil {
		return ErrorResult(fmt.Sprintf("failed to remove cron job: %v", err))
	}

	data, _ := json.MarshalIndent(map[string]any{"deleted": true, "jobId": jobID}, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleRun(ctx context.Context, args map[string]any, agentID, userID string) *Result {
	jobID := resolveJobID(args)
	if jobID == "" {
		return ErrorResult("jobId is required for run action")
	}

	if _, errResult := t.checkJobOwnership(ctx, jobID, agentID, userID); errResult != nil {
		return errResult
	}

	runMode, _ := args["runMode"].(string)
	force := runMode == "force"

	ran, reason, err := t.cronStore.RunJob(ctx, jobID, force)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to run cron job: %v", err))
	}

	result := map[string]any{
		"ran":   ran,
		"jobId": jobID,
	}
	if !ran && reason != "" {
		result["reason"] = reason
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleRuns(ctx context.Context, args map[string]any, agentID, userID string) *Result {
	jobID := resolveJobID(args)

	// Validate ownership if a specific job is requested
	if jobID != "" {
		if _, errResult := t.checkJobOwnership(ctx, jobID, agentID, userID); errResult != nil {
			return errResult
		}
	}

	limit := 20
	if v, ok := numberFromMap(args, "limit"); ok {
		limit = int(v)
	}

	entries, total := t.cronStore.GetRunLog(ctx, jobID, limit, 0)

	result := map[string]any{
		"entries": entries,
		"count":   len(entries),
		"total":   total,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return NewResult(string(data))
}

// --- helpers ---

func resolveJobID(args map[string]any) string {
	if id, ok := args["jobId"].(string); ok && id != "" {
		return id
	}
	if id, ok := args["id"].(string); ok && id != "" {
		return id
	}
	return ""
}

func stringFromMap(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func numberFromMap(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

func adjustNearPastAtMS(ms, now int64) (int64, bool) {
	if ms <= 0 {
		return 0, false
	}
	const nearPastWindow = int64(10 * time.Minute / time.Millisecond)
	if now-ms > nearPastWindow {
		return 0, false
	}
	return now + int64(60*time.Second/time.Millisecond), true
}

func isDuplicateCronJobNameError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") && strings.Contains(msg, "cron") && strings.Contains(msg, "name")
}

func uniqueCronJobName(name string, now time.Time) string {
	suffix := now.Format("20060102-150405")
	if len(name)+1+len(suffix) <= 120 {
		return name + "-" + suffix
	}
	keep := 120 - 1 - len(suffix)
	if keep < 1 {
		return suffix
	}
	return strings.TrimRight(name[:keep], "-") + "-" + suffix
}

func isInternalCronChannel(channel string) bool {
	switch channel {
	case "cli", "system", "subagent", "cron", "teammate", "http", "api", "wake":
		return true
	default:
		return false
	}
}

func normalizeCronTimezone(tz string) string {
	if tz == "Asia/Ho_Chi_Minh" {
		return "Asia/Saigon"
	}
	return tz
}

func parseAtLocalMS(atLocal string, loc *time.Location) (int64, error) {
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04",
	}
	for _, format := range formats {
		if t, err := time.ParseInLocation(format, atLocal, loc); err == nil {
			return t.UnixMilli(), nil
		}
	}
	if t, err := time.Parse(time.RFC3339, atLocal); err == nil {
		return t.UnixMilli(), nil
	}
	return 0, fmt.Errorf("unsupported local time %q", atLocal)
}
