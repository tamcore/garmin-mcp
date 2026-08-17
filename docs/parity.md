# Upstream parity matrix

Authoritative compatibility inventory of the upstream Python MCP server, extracted on 2026-08-14
from [`Taxuspt/garmin_mcp@3610be6feed9`](https://github.com/Taxuspt/garmin_mcp/commit/3610be6feed93088d85b0f35aba9d7d07c2505a7).

Machine-readable source of truth: [`compat/tools.json`](../compat/tools.json) and
[`compat/resources.json`](../compat/resources.json). This document is the human-readable view of those files.

## Measured totals

- **138** `@app.tool()` registrations under `src/garmin_mcp`.
- **5** `@app.resource(...)` registrations, all in `src/garmin_mcp/workout_templates.py`.

## Implementation status

Measured against the registry in `internal/tools/register.go` on 2026-08-15.
The counts below are the same numbers as the `counts.byStatus` block of
`compat/tools.json`, and `internal/tools/manifest_status_test.go` fails the build
when the manifest status and the registered surface disagree either way.

| | Count |
| --- | --- |
| Manifest tools implemented | **137** of 138 |
| Manifest tools not implemented | 1 (`set_fit_download_dir`) |
| Manifest resources implemented | **5** of 5 |
| Tools registered beyond the manifest | 6, one of them the server's own `server_info` |
| Tools registered in total | 143 |

The 143 registered tools are 99 read-only, 35 write and 9 destructive. Read-only
tools always register. Write and destructive tools register too, so the policy
has a tool to refuse and the start-up tier validation covers them, and they are
gated at call time on the intersection of operator enablement and a granted
scope.

**Registration is not the same as advertisement.** `tools/list` narrows this
143-tool registry to what `policy.Decide` would actually allow the calling
session: a stdio session sees only the read-only tier plus `server_info`; a
remote session sees more only once the operator has enabled a higher tier *and*
the caller's own token carries that tier's scope. The filter runs the identical
`Decide` the call path uses — never a parallel classification — so a tool
`tools/list` shows is always one `Decide` allows. For a destructive tool that is
one gate short of "the same session could then call it end to end": calling it
also needs the client to confirm through MCP elicitation, which fails closed
when the client declares no elicitation capability at all, so `tools/list`
additionally withholds a destructive tool from a client that declared none —
the identical capability check `internal/mcpserver/confirm.go` applies at call
time. Once that capability is declared, a tool `tools/list` shows is one the
same session could actually call end to end. `server_info` reports the caller's
effective tiers (a tier is named only when at least one of its tools would
pass `Decide`, so it also reflects the operator's tool allowlist/denylist, not
only enablement and scope), granted scopes, and how many tools it would see
(`visibleToolCount`, narrowed by the same elicitation check), alongside the
unfiltered registered total (`toolCount`). Each tool's `_meta.tier` on the wire
names its policy tier. See `docs/operations.md` for the deployment-shape table.

Per-tool status is the `Status` column of [Tools](#tools) below. The Go handler
for each implemented tool is in
[Implemented tools and their Go handlers](#implemented-tools-and-their-go-handlers),
and every deliberate difference from the upstream contract is in
[Deliberate deviations](#deliberate-deviations).

The upstream README still advertises "110+" tools at this commit. The strict source inventory above
is the number that governs parity work; the README count is not used.

All 138 tools register unconditionally by default. Upstream `_ToolFilter` can suppress registrations
at runtime through `GARMIN_ENABLED_TOOLS` / `GARMIN_DISABLED_TOOLS`, and no registration uses an explicit
`@app.tool(name=...)`, so every MCP tool name equals its Python function name.

## Totals by effect class

| Effect | Tools |
| --- | --- |
| destructive | 8 |
| external-side-effect | 2 |
| read-only | 97 |
| write | 31 |

## Totals by sensitivity

| Sensitivity | Tools |
| --- | --- |
| device | 10 |
| health | 84 |
| location | 14 |
| nutrition | 14 |
| ordinary | 9 |
| profile | 4 |
| womens-health | 3 |

## Totals by primary scope

| Scope | Tools |
| --- | --- |
| `garmin:activities:destructive` | 1 |
| `garmin:activities:read` | 16 |
| `garmin:activities:write` | 8 |
| `garmin:challenges:read` | 6 |
| `garmin:devices:read` | 8 |
| `garmin:devices:write` | 2 |
| `garmin:health:destructive` | 1 |
| `garmin:health:read` | 48 |
| `garmin:health:write` | 6 |
| `garmin:nutrition:destructive` | 2 |
| `garmin:nutrition:read` | 6 |
| `garmin:nutrition:write` | 6 |
| `garmin:profile:read` | 4 |
| `garmin:womens-health:read` | 3 |
| `garmin:workouts:destructive` | 4 |
| `garmin:workouts:read` | 6 |
| `garmin:workouts:write` | 9 |
| `local:files:write` | 2 |

`scope` is the single primary gate per tool. `scopesRequired` is the full set a
caller must hold, so the counts below are larger than the totals above wherever a
payload spans two sensitivity domains.

| Scope | Tools requiring it |
| --- | --- |
| `garmin:activities:destructive` | 1 |
| `garmin:activities:read` | 17 |
| `garmin:activities:write` | 8 |
| `garmin:challenges:read` | 6 |
| `garmin:devices:read` | 8 |
| `garmin:devices:write` | 2 |
| `garmin:health:destructive` | 5 |
| `garmin:health:read` | 74 |
| `garmin:health:write` | 18 |
| `garmin:nutrition:destructive` | 2 |
| `garmin:nutrition:read` | 6 |
| `garmin:nutrition:write` | 6 |
| `garmin:profile:read` | 4 |
| `garmin:womens-health:read` | 3 |
| `garmin:workouts:destructive` | 4 |
| `garmin:workouts:read` | 6 |
| `garmin:workouts:write` | 9 |
| `local:files:read` | 1 |
| `local:files:write` | 2 |

## Totals by idempotency

| Idempotency | Tools |
| --- | --- |
| idempotent | 118 |
| non-idempotent | 20 |

`idempotent` means replaying the call converges on the same end state. A convergence claim that
rests on a pre-check which fails open is **not** idempotency: those tools are `non-idempotent`, and
their `idempotencyNote` records what the pre-check does, when it fails open, and the retry policy
that follows. See [Scheduling is not idempotent](#scheduling-is-not-idempotent).

## Totals by authorization

Two independent fields, one per question. Neither implies the other.

| Field | Question it answers | Meaning of `true` | Meaning of `false` |
| --- | --- | --- | --- |
| `authRequired` | Does invoking this perform a Garmin-authenticated call? | At least one Garmin HTTP call is reachable, so a valid Garmin session or credential must exist server-side. | No Garmin call happens at all. It does **not** mean the record is unauthenticated at the MCP layer. |
| `mcpAuthorizationRequired` | Must the MCP caller be authorized and hold `scopesRequired`? | The server refuses to dispatch without an authorized caller holding every scope in `scopesRequired`. | Would mean an anonymous, unscoped entry point. No record on this surface is `false`. |

Both fields are defined in the `fieldDefinitions` block of `compat/tools.json` and
`compat/resources.json`, alongside `scope`, `scopesRequired`, and `idempotency`.

| `authRequired` | Tools | Resources |
| --- | --- | --- |
| `true` | 137 | 0 |
| `false` | 1 | 5 |

| `mcpAuthorizationRequired` | Tools | Resources |
| --- | --- | --- |
| `true` | 138 | 5 |
| `false` | 0 | 0 |

The 6 records with `authRequired: false` are `set_fit_download_dir` and the 5 static workout-template resources. Every one of them still carries
`mcpAuthorizationRequired: true` and a required scope, which is deliberate and not a contradiction:
`set_fit_download_dir` writes to the server filesystem and persists configuration, so it must not be
invokable without authorization even though Garmin is never contacted, and the resources keep one
fail-closed registration path with no "no scope required" branch. The scope on these records is
enforced by this server, not by Garmin.

## Totals by upstream module

| Module | Tools |
| --- | --- |
| activity_analysis.py | 4 |
| activity_management.py | 21 |
| challenges.py | 9 |
| courses.py | 3 |
| data_management.py | 3 |
| devices.py | 6 |
| gear_management.py | 3 |
| health_wellness.py | 29 |
| nutrition.py | 14 |
| training.py | 15 |
| user_profile.py | 4 |
| weight_management.py | 5 |
| womens_health.py | 3 |
| workout_builders.py | 5 |
| workouts.py | 14 |

### Class definitions

| Effect | Meaning |
| --- | --- |
| `read-only` | Reads Garmin state only. |
| `write` | Creates or updates Garmin-side state. |
| `destructive` | Removes Garmin-side user data. |
| `external-side-effect` | Acts outside Garmin (local filesystem). |

| Sensitivity | Meaning |
| --- | --- |
| `health` | Physiological or medical data: heart rate, HRV, sleep, stress, SpO2, weight, blood pressure, training load. |
| `location` | GPS traces, coordinates, courses, or activity records that embed position. |
| `profile` | Account identity and user settings. |
| `device` | Device inventory, settings, alarms, and telemetry. |
| `nutrition` | Food logs, custom foods, and nutrition settings. |
| `womens-health` | Menstrual and pregnancy data. |
| `ordinary` | Metadata with no special category: earned badges, activity naming and typing, static catalogs. |

`sensitivity` in the JSON is the primary class; `sensitivityTags` lists every class that applies.
`ordinary` is exclusive: it appears only when no other class applies, so no record carries
`ordinary` next to a real class.

A record is `health` when its payload is, or directly embeds, a physiological or medical
measurement, a fitness-state estimate, or a dated personal training prescription or activity
record. That covers manual activity logging, goals, personal records, race predictions, workout
definitions (they carry the caller's own heart-rate zones, bpm ranges, paces, and power targets),
the training calendar, Garmin Coach plans, and challenge listings that report the caller's own
progress value. Gear is `device`: it is user equipment inventory.

### Scopes

Scopes are the enforceable boundary; sensitivity tags are metadata that explain why a scope is
required. Both are kept. Two fields carry the map:

- `scope` — the single primary gate. It names the functional domain that owns the object the tool
  acts on, plus the action implied by the effect class (`read-only` to `read`, `write` to `write`,
  `destructive` to `destructive`).
- `scopesRequired` — every scope the caller must hold, all of them, not any of them. It adds one
  scope per further sensitivity class in the payload, so a health-bearing activity read needs the
  activities read scope and the health read scope, and a health-bearing activity write needs both
  write scopes.

| Scope | Grants |
| --- | --- |
| `garmin:profile:read` | Account identity, unit system, and profile settings. |
| `garmin:activities:read` | Activity lists and details, splits, weather, FIT parsing, power curves, courses. Covers position data. |
| `garmin:activities:write` | Create a manual activity, rename/retype/annotate an activity, upload a course. |
| `garmin:activities:destructive` | Delete a course. |
| `garmin:health:read` | Physiological and wellness series and summaries, weight, blood pressure, training load, HRV, fitness estimates, goals, and personal records. |
| `garmin:health:write` | Write a health record: body composition, blood pressure, hydration, weigh-ins, perceived effort, how an activity felt, manual activity, epoch reload. |
| `garmin:health:destructive` | Delete health records: weigh-ins for a date, and workout/calendar records that are classified `health`. |
| `garmin:devices:read` | Device inventory, settings, alarms, solar telemetry, and gear inventory. |
| `garmin:devices:write` | Attach or detach gear on an activity. |
| `garmin:nutrition:read` | Food logs, meals, nutrition settings, food search, custom foods. |
| `garmin:nutrition:write` | Create or update custom foods, log food, update nutrition targets. |
| `garmin:nutrition:destructive` | Delete a custom food or a food-log entry. |
| `garmin:womens-health:read` | Menstrual and pregnancy data. |
| `garmin:workouts:read` | Workout library and definitions, FIT workout download, training calendar, Garmin Coach plans, workout templates. |
| `garmin:workouts:write` | Upload or build a workout, schedule a workout or a week. |
| `garmin:workouts:destructive` | Delete a workout, or remove a calendar entry. |
| `garmin:challenges:read` | Badge and virtual challenge listings, earned badges, ad-hoc social challenges. |
| `local:files:read` | Read a caller-named path on the server filesystem. |
| `local:files:write` | Write to, or configure, a path on the server filesystem. |

Rules that follow from the map:

- Read scopes separate profile, activities/location, health, devices, nutrition, and women's health,
  and additionally separate workouts and challenges. `garmin:read` is allowed only as an aggregate
  compatibility scope, only expanded to those eight read scopes, and only when consent shows the
  expansion explicitly. It is listed in `enums.scopeAggregates` and is never a tool's `scope`.
- Write and destructive scopes stay separate from reads and from each other, per domain.
- Remote deployments default to read-only. A write tool needs a granted write scope **and** operator
  enablement; a destructive tool needs the destructive scope, operator enablement, and confirmation.
- `local:files:read` and `local:files:write` gate the two `external-side-effect` tools and the local
  GPX read in `upload_course`. They are not Garmin scopes and must never be granted to a remote
  deployment that lets a caller name a server path. `set_fit_download_dir` never calls Garmin, so it
  carries no Garmin scope at all.
- Allowlists and denylists are intersected with the granted scopes; they never widen them.
- The five resources carry no user data and make no Garmin call (`authRequired: false`), but they are
  still gated on `garmin:workouts:read` with `mcpAuthorizationRequired: true` so registration keeps one
  fail-closed path with no "no scope required" branch. See
  [Totals by authorization](#totals-by-authorization) for why those two values are consistent.

## Tools

This table is the tool-to-scope map. `Scope` is the primary gate; tools that need more than that
one scope are listed under [Tools requiring more than one scope](#tools-requiring-more-than-one-scope).

`Status` is this server's state. **`Effect`, `Sensitivity`, `Scope` and `Idempotency` are the
upstream manifest's classification**, not a description of this server's behavior, and three
implemented rows deliberately differ:

- `download_activity_file` is `external-side-effect` / `local:files:write` here and is registered in
  the **write** tier by this server, which writes no file at all;
- `schedule_workout` and `schedule_workouts` are `non-idempotent` in both, but this server also drops
  the duplicate-avoidance pre-check, so they are less convergent than upstream.

See [Deliberate deviations](#deliberate-deviations).

| Tool | Status | Effect | Sensitivity | Scope | Idempotency | Upstream |
| --- | --- | --- | --- | --- | --- | --- |
| `add_body_composition` | **implemented** | write | health | `garmin:health:write` | non-idempotent | data_management.py:22 |
| `add_gear_to_activity` | **implemented** | write | device | `garmin:devices:write` | idempotent | gear_management.py:157 |
| `add_hydration_data` | **implemented** | write | health | `garmin:health:write` | non-idempotent | data_management.py:98 |
| `add_weigh_in` | **implemented** | write | health | `garmin:health:write` | non-idempotent | weight_management.py:156 |
| `add_weigh_in_with_timestamps` | **implemented** | write | health | `garmin:health:write` | non-idempotent | weight_management.py:176 |
| `count_activities` | **implemented** | read-only | ordinary | `garmin:activities:read` | idempotent | activity_management.py:812 |
| `create_custom_food` | **implemented** | write | nutrition | `garmin:nutrition:write` | non-idempotent | nutrition.py:269 |
| `create_manual_activity` | **implemented** | write | health | `garmin:activities:write` | non-idempotent | activity_management.py:892 |
| `create_run_workout` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:392 |
| `create_strength_workout` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:484 |
| `create_walk_run_workout` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:344 |
| `create_z2_walk_workout` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:447 |
| `delete_course` | **implemented** | destructive | location | `garmin:activities:destructive` | idempotent | courses.py:289 |
| `delete_custom_food` | **implemented** | destructive | nutrition | `garmin:nutrition:destructive` | idempotent | nutrition.py:518 |
| `delete_food_log` | **implemented** | destructive | nutrition | `garmin:nutrition:destructive` | idempotent | nutrition.py:720 |
| `delete_weigh_ins` | **implemented** | destructive | health | `garmin:health:destructive` | idempotent | weight_management.py:136 |
| `delete_workout` | **implemented** | destructive | health | `garmin:workouts:destructive` | idempotent | workouts.py:996 |
| `delete_workouts` | **implemented** | destructive | health | `garmin:workouts:destructive` | idempotent | workouts.py:1023 |
| `download_activity_file` | **implemented** | external-side-effect | location | `local:files:write` | idempotent | activity_analysis.py:1263 |
| `download_workout` | **implemented** | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:768 |
| `get_activities` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:830 |
| `get_activities_by_date` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:50 |
| `get_activities_fordate` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:161 |
| `get_activity` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:210 |
| `get_activity_exercise_sets` | **implemented** | read-only | health | `garmin:activities:read` | idempotent | activity_management.py:795 |
| `get_activity_fit_data` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_analysis.py:1053 |
| `get_activity_gear` | **implemented** | read-only | device | `garmin:devices:read` | idempotent | activity_management.py:778 |
| `get_activity_hr_in_timezones` | **implemented** | read-only | health | `garmin:activities:read` | idempotent | activity_management.py:741 |
| `get_activity_power_in_timezones` | **implemented** | read-only | health | `garmin:activities:read` | idempotent | activity_management.py:758 |
| `get_activity_split_summaries` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:638 |
| `get_activity_splits` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:539 |
| `get_activity_typed_splits` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:621 |
| `get_activity_types` | **implemented** | read-only | ordinary | `garmin:activities:read` | idempotent | activity_management.py:942 |
| `get_activity_weather` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:655 |
| `get_adhoc_challenges` | **implemented** | read-only | ordinary | `garmin:challenges:read` | idempotent | challenges.py:363 |
| `get_all_day_events` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:687 |
| `get_all_day_stress` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:671 |
| `get_available_badge_challenges` | **implemented** | read-only | health | `garmin:challenges:read` | idempotent | challenges.py:412 |
| `get_badge_challenges` | **implemented** | read-only | health | `garmin:challenges:read` | idempotent | challenges.py:445 |
| `get_blood_pressure` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:309 |
| `get_body_battery` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:247 |
| `get_body_battery_events` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:293 |
| `get_body_composition` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:115 |
| `get_courses` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | courses.py:173 |
| `get_custom_food_serving_units` | **implemented** | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:253 |
| `get_custom_foods` | **implemented** | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:220 |
| `get_cycling_ftp` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:647 |
| `get_daily_steps` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:172 |
| `get_daily_weigh_ins` | **implemented** | read-only | health | `garmin:health:read` | idempotent | weight_management.py:86 |
| `get_device_alarms` | **implemented** | read-only | device | `garmin:devices:read` | idempotent | devices.py:279 |
| `get_device_last_used` | **implemented** | read-only | device | `garmin:devices:read` | idempotent | devices.py:62 |
| `get_device_settings` | **implemented** | read-only | device | `garmin:devices:read` | idempotent | devices.py:95 |
| `get_device_solar_data` | **implemented** | read-only | device | `garmin:devices:read` | idempotent | devices.py:229 |
| `get_devices` | **implemented** | read-only | device | `garmin:devices:read` | idempotent | devices.py:23 |
| `get_earned_badges` | **implemented** | read-only | ordinary | `garmin:challenges:read` | idempotent | challenges.py:297 |
| `get_endurance_score` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:274 |
| `get_fitnessage_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:487 |
| `get_floors` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:326 |
| `get_full_name` | **implemented** | read-only | profile | `garmin:profile:read` | idempotent | user_profile.py:22 |
| `get_garmin_coach_workouts` | **implemented** | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:1098 |
| `get_gear` | **implemented** | read-only | device | `garmin:devices:read` | idempotent | gear_management.py:42 |
| `get_goals` | **implemented** | read-only | health | `garmin:health:read` | idempotent | challenges.py:237 |
| `get_heart_rates` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:358 |
| `get_heart_rates_summary` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:377 |
| `get_hill_score` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:217 |
| `get_hrv_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:431 |
| `get_hrv_trend` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:998 |
| `get_hydration_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:415 |
| `get_inprogress_virtual_challenges` | **implemented** | read-only | health | `garmin:challenges:read` | idempotent | challenges.py:552 |
| `get_lactate_threshold` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:675 |
| `get_lifestyle_logging_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:703 |
| `get_menstrual_calendar_data` | **implemented** | read-only | womens-health | `garmin:womens-health:read` | idempotent | womens_health.py:75 |
| `get_menstrual_data_for_date` | **implemented** | read-only | womens-health | `garmin:womens-health:read` | idempotent | womens_health.py:60 |
| `get_morning_training_readiness` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:867 |
| `get_non_completed_badge_challenges` | **implemented** | read-only | health | `garmin:challenges:read` | idempotent | challenges.py:478 |
| `get_nutrition_daily_food_log` | **implemented** | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:32 |
| `get_nutrition_daily_meals` | **implemented** | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:51 |
| `get_nutrition_daily_settings` | **implemented** | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:71 |
| `get_personal_record` | **implemented** | read-only | health | `garmin:health:read` | idempotent | challenges.py:252 |
| `get_power_duration_curve` | **implemented** | read-only | location | `garmin:activities:read` | idempotent | activity_analysis.py:1150 |
| `get_pregnancy_summary` | **implemented** | read-only | womens-health | `garmin:womens-health:read` | idempotent | womens_health.py:49 |
| `get_primary_training_device` | **implemented** | read-only | device | `garmin:devices:read` | idempotent | devices.py:177 |
| `get_progress_summary_between_dates` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:161 |
| `get_race_predictions` | **implemented** | read-only | health | `garmin:health:read` | idempotent | challenges.py:513 |
| `get_respiration_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:588 |
| `get_respiration_summary` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:607 |
| `get_respiration_trend` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:1227 |
| `get_rhr_day` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:342 |
| `get_scheduled_workouts` | **implemented** | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:1059 |
| `get_sleep_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:431 |
| `get_sleep_summary` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:450 |
| `get_spo2_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:636 |
| `get_stats` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:22 |
| `get_stats_and_body` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:137 |
| `get_steps_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:153 |
| `get_stress_data` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:524 |
| `get_stress_summary` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:543 |
| `get_training_effect` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:388 |
| `get_training_load_balance` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:899 |
| `get_training_load_trend` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:791 |
| `get_training_plan_workouts` | **implemented** | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:1132 |
| `get_training_readiness` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:189 |
| `get_training_status` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:566 |
| `get_unit_system` | **implemented** | read-only | profile | `garmin:profile:read` | idempotent | user_profile.py:31 |
| `get_user_profile` | **implemented** | read-only | profile | `garmin:profile:read` | idempotent | user_profile.py:40 |
| `get_user_summary` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:99 |
| `get_userprofile_settings` | **implemented** | read-only | profile | `garmin:profile:read` | idempotent | user_profile.py:51 |
| `get_vo2max_trend` | **implemented** | read-only | health | `garmin:health:read` | idempotent | training.py:1072 |
| `get_weekly_intensity_minutes` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:811 |
| `get_weekly_steps` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:722 |
| `get_weekly_stress` | **implemented** | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:769 |
| `get_weigh_ins` | **implemented** | read-only | health | `garmin:health:read` | idempotent | weight_management.py:22 |
| `get_workout_by_id` | **implemented** | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:729 |
| `get_workouts` | **implemented** | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:707 |
| `log_custom_food` | **implemented** | write | nutrition | `garmin:nutrition:write` | non-idempotent | nutrition.py:546 |
| `log_food` | **implemented** | write | nutrition | `garmin:nutrition:write` | non-idempotent | nutrition.py:637 |
| `remove_gear_from_activity` | **implemented** | write | device | `garmin:devices:write` | idempotent | gear_management.py:182 |
| `request_reload` | **implemented** | write | health | `garmin:health:write` | idempotent | training.py:778 |
| `schedule_week` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:522 |
| `schedule_workout` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workouts.py:1154 |
| `schedule_workouts` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workouts.py:1212 |
| `search_foods` | **implemented** | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:144 |
| `set_activity_description` | **implemented** | write | ordinary | `garmin:activities:write` | idempotent | activity_management.py:386 |
| `set_activity_event_type` | **implemented** | write | ordinary | `garmin:activities:write` | idempotent | activity_management.py:416 |
| `set_activity_feel` | **implemented** | write | health | `garmin:activities:write` | idempotent | activity_management.py:503 |
| `set_activity_name` | **implemented** | write | ordinary | `garmin:activities:write` | idempotent | activity_management.py:313 |
| `set_activity_type` | **implemented** | write | ordinary | `garmin:activities:write` | idempotent | activity_management.py:341 |
| `set_blood_pressure` | **implemented** | write | health | `garmin:health:write` | non-idempotent | data_management.py:75 |
| `set_fit_download_dir` | not-implemented | external-side-effect | ordinary | `local:files:write` | idempotent | activity_analysis.py:1363 |
| `set_nutrition_daily_settings` | **implemented** | write | nutrition | `garmin:nutrition:write` | idempotent | nutrition.py:90 |
| `set_perceived_effort` | **implemented** | write | health | `garmin:activities:write` | idempotent | activity_management.py:467 |
| `unschedule_workout` | **implemented** | destructive | health | `garmin:workouts:destructive` | idempotent | workouts.py:1345 |
| `unschedule_workouts` | **implemented** | destructive | health | `garmin:workouts:destructive` | idempotent | workouts.py:1381 |
| `update_custom_food` | **implemented** | write | nutrition | `garmin:nutrition:write` | idempotent | nutrition.py:376 |
| `upload_course` | **implemented** | write | location | `garmin:activities:write` | non-idempotent | courses.py:203 |
| `upload_workout` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workouts.py:794 |
| `upload_workouts` | **implemented** | write | health | `garmin:workouts:write` | non-idempotent | workouts.py:933 |
| `upsert_and_log` | **implemented** | write | nutrition | `garmin:nutrition:write` | non-idempotent | nutrition.py:745 |

### Tools requiring more than one scope

43 of 138 tools need every scope listed, not just the primary one.

| Tool | Primary scope | All required scopes |
| --- | --- | --- |
| `create_manual_activity` | `garmin:activities:write` | `garmin:activities:write`, `garmin:health:write` |
| `create_run_workout` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |
| `create_strength_workout` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |
| `create_walk_run_workout` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |
| `create_z2_walk_workout` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |
| `delete_workout` | `garmin:workouts:destructive` | `garmin:health:destructive`, `garmin:workouts:destructive` |
| `delete_workouts` | `garmin:workouts:destructive` | `garmin:health:destructive`, `garmin:workouts:destructive` |
| `download_activity_file` | `local:files:write` | `garmin:activities:read`, `garmin:health:read`, `local:files:write` |
| `download_workout` | `garmin:workouts:read` | `garmin:health:read`, `garmin:workouts:read` |
| `get_activities` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activities_by_date` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activities_fordate` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activity` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activity_exercise_sets` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activity_fit_data` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activity_hr_in_timezones` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activity_power_in_timezones` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activity_split_summaries` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activity_splits` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_activity_typed_splits` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_available_badge_challenges` | `garmin:challenges:read` | `garmin:challenges:read`, `garmin:health:read` |
| `get_badge_challenges` | `garmin:challenges:read` | `garmin:challenges:read`, `garmin:health:read` |
| `get_garmin_coach_workouts` | `garmin:workouts:read` | `garmin:health:read`, `garmin:workouts:read` |
| `get_inprogress_virtual_challenges` | `garmin:challenges:read` | `garmin:challenges:read`, `garmin:health:read` |
| `get_menstrual_calendar_data` | `garmin:womens-health:read` | `garmin:health:read`, `garmin:womens-health:read` |
| `get_menstrual_data_for_date` | `garmin:womens-health:read` | `garmin:health:read`, `garmin:womens-health:read` |
| `get_non_completed_badge_challenges` | `garmin:challenges:read` | `garmin:challenges:read`, `garmin:health:read` |
| `get_power_duration_curve` | `garmin:activities:read` | `garmin:activities:read`, `garmin:health:read` |
| `get_pregnancy_summary` | `garmin:womens-health:read` | `garmin:health:read`, `garmin:womens-health:read` |
| `get_scheduled_workouts` | `garmin:workouts:read` | `garmin:health:read`, `garmin:workouts:read` |
| `get_training_plan_workouts` | `garmin:workouts:read` | `garmin:health:read`, `garmin:workouts:read` |
| `get_workout_by_id` | `garmin:workouts:read` | `garmin:health:read`, `garmin:workouts:read` |
| `get_workouts` | `garmin:workouts:read` | `garmin:health:read`, `garmin:workouts:read` |
| `schedule_week` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |
| `schedule_workout` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |
| `schedule_workouts` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |
| `set_activity_feel` | `garmin:activities:write` | `garmin:activities:write`, `garmin:health:write` |
| `set_perceived_effort` | `garmin:activities:write` | `garmin:activities:write`, `garmin:health:write` |
| `unschedule_workout` | `garmin:workouts:destructive` | `garmin:health:destructive`, `garmin:workouts:destructive` |
| `unschedule_workouts` | `garmin:workouts:destructive` | `garmin:health:destructive`, `garmin:workouts:destructive` |
| `upload_course` | `garmin:activities:write` | `garmin:activities:write`, `local:files:read` |
| `upload_workout` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |
| `upload_workouts` | `garmin:workouts:write` | `garmin:health:write`, `garmin:workouts:write` |

## Implemented tools and their Go handlers

Every row is registered by `internal/tools/register.go` in tier order. Paths are
relative to the repository root.

### Read-only tier — 98 tools

| Tool | Go registrar | File |
| --- | --- | --- |
| `get_user_profile` | `registerGetUserProfile` | `internal/tools/get_user_profile.go` |
| `get_full_name` | `registerGetFullName` | `internal/tools/get_full_name.go` |
| `get_unit_system` | `registerGetUnitSystem` | `internal/tools/get_unit_system.go` |
| `get_activities` | `registerGetActivities` | `internal/tools/get_activities.go` |
| `get_activities_by_date` | `registerGetActivitiesByDate` | `internal/tools/get_activities_by_date.go` |
| `get_activities_fordate` | `registerGetActivitiesForDate` | `internal/tools/get_activities_fordate.go` |
| `count_activities` | `registerCountActivities` | `internal/tools/count_activities.go` |
| `get_activity` | `registerGetActivity` | `internal/tools/get_activity.go` |
| `get_activity_gear` | `registerGetActivityGear` | `internal/tools/get_activity_gear.go` |
| `get_activity_types` | `registerGetActivityTypes` | `internal/tools/get_activity_types.go` |
| `get_sleep_data` | `registerGetSleepData` | `internal/tools/get_sleep_data.go` |
| `get_user_summary` | `registerGetUserSummary` | `internal/tools/get_user_summary.go` |
| `get_stats` | `registerGetStats` | `internal/tools/get_stats.go` |
| `get_stats_and_body` | `registerGetStatsAndBody` | `internal/tools/get_stats_and_body.go` |
| `get_body_composition` | `registerGetBodyComposition` | `internal/tools/get_body_composition.go` |
| `get_steps_data` | `registerGetStepsData` | `internal/tools/get_steps_data.go` |
| `get_daily_steps` | `registerGetDailySteps` | `internal/tools/get_daily_steps.go` |
| `get_weekly_steps` | `registerGetWeeklySteps` | `internal/tools/get_weekly_steps.go` |
| `get_floors` | `registerGetFloors` | `internal/tools/get_floors.go` |
| `get_weekly_intensity_minutes` | `registerGetWeeklyIntensityMinutes` | `internal/tools/get_weekly_intensity_minutes.go` |
| `get_stress_data` | `registerGetStressData` | `internal/tools/get_stress_data.go` |
| `get_stress_summary` | `registerGetStressSummary` | `internal/tools/get_stress_summary.go` |
| `get_all_day_stress` | `registerGetAllDayStress` | `internal/tools/get_all_day_stress.go` |
| `get_weekly_stress` | `registerGetWeeklyStress` | `internal/tools/get_weekly_stress.go` |
| `get_body_battery` | `registerGetBodyBattery` | `internal/tools/get_body_battery.go` |
| `get_body_battery_events` | `registerGetBodyBatteryEvents` | `internal/tools/get_body_battery_events.go` |
| `get_training_readiness` | `registerGetTrainingReadiness` | `internal/tools/get_training_readiness.go` |
| `get_morning_training_readiness` | `registerGetMorningTrainingReadiness` | `internal/tools/get_morning_training_readiness.go` |
| `get_all_day_events` | `registerGetAllDayEvents` | `internal/tools/get_all_day_events.go` |
| `get_heart_rates` | `registerGetHeartRates` | `internal/tools/get_heart_rates.go` |
| `get_heart_rates_summary` | `registerGetHeartRatesSummary` | `internal/tools/get_heart_rates_summary.go` |
| `get_rhr_day` | `registerGetRestingHeartRateDay` | `internal/tools/get_rhr_day.go` |
| `get_respiration_data` | `registerGetRespirationData` | `internal/tools/get_respiration_data.go` |
| `get_respiration_summary` | `registerGetRespirationSummary` | `internal/tools/get_respiration_summary.go` |
| `get_spo2_data` | `registerGetSpO2Data` | `internal/tools/get_spo2_data.go` |
| `get_sleep_summary` | `registerGetSleepSummary` | `internal/tools/get_sleep_summary.go` |
| `get_blood_pressure` | `registerGetBloodPressure` | `internal/tools/get_blood_pressure.go` |
| `get_hydration_data` | `registerGetHydrationData` | `internal/tools/get_hydration_data.go` |
| `get_lifestyle_logging_data` | `registerGetLifestyleLoggingData` | `internal/tools/get_lifestyle_logging_data.go` |
| `get_devices` | `registerGetDevices` | `internal/tools/get_devices.go` |
| `get_activity_typed_splits` | `registerGetActivityTypedSplits` | `internal/tools/get_activity_typed_splits.go` |
| `get_activity_exercise_sets` | `registerGetActivityExerciseSets` | `internal/tools/get_activity_exercise_sets.go` |
| `get_userprofile_settings` | `registerGetUserProfileSettings` | `internal/tools/profilereads.go` |
| `get_personal_record` | `registerGetPersonalRecord` | `internal/tools/profilereads.go` |
| `get_activity_splits` | `registerGetActivitySplits` | `internal/tools/analysis.go` |
| `get_activity_split_summaries` | `registerGetActivitySplitSummaries` | `internal/tools/analysis.go` |
| `get_activity_hr_in_timezones` | `registerGetActivityHRInZones` | `internal/tools/analysis.go` |
| `get_activity_power_in_timezones` | `registerGetActivityPowerInZones` | `internal/tools/analysis.go` |
| `get_activity_weather` | `registerGetActivityWeather` | `internal/tools/get_activity_weather.go` |
| `get_activity_fit_data` | `registerGetActivityFITData` | `internal/tools/get_activity_fit_data.go` |
| `get_power_duration_curve` | `registerGetPowerDurationCurve` | `internal/tools/get_power_duration_curve.go` |
| `get_exercise_types` † | `registerGetExerciseTypes` | `internal/tools/builders_strength.go` |
| `get_workouts` | `registerGetWorkouts` | `internal/tools/workoutreads.go` |
| `get_workout_by_id` | `registerGetWorkoutByID` | `internal/tools/workoutreads.go` |
| `download_workout` | `registerDownloadWorkout` | `internal/tools/workoutreads.go` |
| `get_scheduled_workouts` | `registerGetScheduledWorkouts` | `internal/tools/calendarreads.go` |
| `get_training_plan_workouts` | `registerGetTrainingPlanWorkouts` | `internal/tools/calendarreads.go` |
| `get_garmin_coach_workouts` | `registerGetGarminCoachWorkouts` | `internal/tools/garmincoach.go` |
| `get_hill_score` | `registerGetHillScore` | `internal/tools/get_hill_score.go` |
| `get_endurance_score` | `registerGetEnduranceScore` | `internal/tools/get_endurance_score.go` |
| `get_training_effect` | `registerGetTrainingEffect` | `internal/tools/get_training_effect.go` |
| `get_fitnessage_data` | `registerGetFitnessAgeData` | `internal/tools/get_fitnessage_data.go` |
| `get_training_status` | `registerGetTrainingStatus` | `internal/tools/get_training_status.go` |
| `get_cycling_ftp` | `registerGetCyclingFTP` | `internal/tools/get_cycling_ftp.go` |
| `get_lactate_threshold` | `registerGetLactateThreshold` | `internal/tools/get_lactate_threshold.go` |
| `get_progress_summary_between_dates` | `registerGetProgressSummaryBetweenDates` | `internal/tools/get_progress_summary_between_dates.go` |
| `get_hrv_data` | `registerGetHRVData` | `internal/tools/get_hrv_data.go` |
| `get_hrv_trend` | `registerGetHRVTrend` | `internal/tools/get_hrv_trend.go` |
| `get_vo2max_trend` | `registerGetVO2MaxTrend` | `internal/tools/get_vo2max_trend.go` |
| `get_respiration_trend` | `registerGetRespirationTrend` | `internal/tools/get_respiration_trend.go` |
| `get_training_load_trend` | `registerGetTrainingLoadTrend` | `internal/tools/get_training_load_trend.go` |
| `get_training_load_balance` | `registerGetTrainingLoadBalance` | `internal/tools/get_training_load_balance.go` |
| `get_nutrition_daily_food_log` | `registerGetNutritionDailyFoodLog` | `internal/tools/get_nutrition_daily_food_log.go` |
| `get_nutrition_daily_meals` | `registerGetNutritionDailyMeals` | `internal/tools/get_nutrition_daily_meals.go` |
| `get_nutrition_daily_settings` | `registerGetNutritionDailySettings` | `internal/tools/nutritionsettings.go` |
| `search_foods` | `registerSearchFoods` | `internal/tools/search_foods.go` |
| `get_custom_foods` | `registerGetCustomFoods` | `internal/tools/get_custom_foods.go` |
| `get_custom_food_serving_units` | `registerGetCustomFoodServingUnits` | `internal/tools/get_custom_food_serving_units.go` |
| `get_earned_badges` | `registerGetEarnedBadges` | `internal/tools/get_earned_badges.go` |
| `get_goals` | `registerGetGoals` | `internal/tools/get_goals.go` |
| `get_adhoc_challenges` | `registerGetAdhocChallenges` | `internal/tools/get_adhoc_challenges.go` |
| `get_available_badge_challenges` | `registerGetAvailableBadgeChallenges` | `internal/tools/badgechallengelists.go` |
| `get_badge_challenges` | `registerGetBadgeChallenges` | `internal/tools/badgechallengelists.go` |
| `get_non_completed_badge_challenges` | `registerGetNonCompletedBadgeChallenges` | `internal/tools/badgechallengelists.go` |
| `get_race_predictions` | `registerGetRacePredictions` | `internal/tools/get_race_predictions.go` |
| `get_inprogress_virtual_challenges` | `registerGetInProgressVirtualChallenges` | `internal/tools/get_inprogress_virtual_challenges.go` |
| `get_device_last_used` | `registerGetDeviceLastUsed` | `internal/tools/get_device_last_used.go` |
| `get_device_settings` | `registerGetDeviceSettings` | `internal/tools/get_device_settings.go` |
| `get_primary_training_device` | `registerGetPrimaryTrainingDevice` | `internal/tools/get_primary_training_device.go` |
| `get_device_solar_data` | `registerGetDeviceSolarData` | `internal/tools/get_device_solar_data.go` |
| `get_device_alarms` | `registerGetDeviceAlarms` | `internal/tools/get_device_alarms.go` |
| `get_gear` | `registerGetGear` | `internal/tools/get_gear.go` |
| `get_menstrual_calendar_data` | `registerGetMenstrualCalendarData` | `internal/tools/get_menstrual_calendar_data.go` |
| `get_menstrual_data_for_date` | `registerGetMenstrualDataForDate` | `internal/tools/get_menstrual_data_for_date.go` |
| `get_pregnancy_summary` | `registerGetPregnancySummary` | `internal/tools/get_pregnancy_summary.go` |
| `get_weigh_ins` | `registerGetWeighIns` | `internal/tools/weighinreads.go` |
| `get_daily_weigh_ins` | `registerGetDailyWeighIns` | `internal/tools/weighinreads.go` |
| `get_courses` | `registerGetCourses` | `internal/tools/getcourses.go` |

### Write tier — 35 tools

| Tool | Go registrar | File |
| --- | --- | --- |
| `request_reload` | `registerRequestReload` | `internal/tools/request_reload.go` |
| `set_nutrition_daily_settings` | `registerSetNutritionDailySettings` | `internal/tools/nutritionsettings.go` |
| `create_custom_food` | `registerCreateCustomFood` | `internal/tools/customfoodwrites.go` |
| `update_custom_food` | `registerUpdateCustomFood` | `internal/tools/customfoodwrites.go` |
| `log_custom_food` | `registerLogCustomFood` | `internal/tools/foodlogwrites.go` |
| `log_food` | `registerLogFood` | `internal/tools/foodlogwrites.go` |
| `upsert_and_log` | `registerUpsertAndLog` | `internal/tools/upsert_and_log.go` |
| `add_weigh_in` | `registerAddWeighIn` | `internal/tools/weighinwrites.go` |
| `add_weigh_in_with_timestamps` | `registerAddWeighInWithTimestamps` | `internal/tools/weighinwrites.go` |
| `upload_course` | `registerUploadCourse` | `internal/tools/coursesupload.go` |
| `add_body_composition` | `registerAddBodyComposition` | `internal/tools/bodycomposition.go` |
| `set_blood_pressure` | `registerSetBloodPressure` | `internal/tools/bloodpressure.go` |
| `add_hydration_data` | `registerAddHydrationData` | `internal/tools/hydration.go` |
| `set_activity_name` | `registerSetActivityName` | `internal/tools/activitywrites.go` |
| `set_activity_type` | `registerSetActivityType` | `internal/tools/activitywrites.go` |
| `set_activity_event_type` | `registerSetActivityEventType` | `internal/tools/activitywrites.go` |
| `set_activity_description` | `registerSetActivityDescription` | `internal/tools/activitywrites.go` |
| `set_activity_feel` | `registerSetActivityFeel` | `internal/tools/activitywrites.go` |
| `set_perceived_effort` | `registerSetPerceivedEffort` | `internal/tools/activitywrites.go` |
| `add_gear_to_activity` | `registerAddGearToActivity` | `internal/tools/gearwrites.go` |
| `remove_gear_from_activity` | `registerRemoveGearFromActivity` | `internal/tools/gearwrites.go` |
| `create_manual_activity` | `registerCreateManualActivity` | `internal/tools/activitylifecycle.go` |
| `set_activity_strength_exercise_sets` † | `registerSetActivityStrengthExerciseSets` | `internal/tools/strengthwrites.go` |
| `create_strength_training_activity` † | `registerCreateStrengthTrainingActivity` | `internal/tools/create_strength_training_activity.go` |
| `upload_workout` | `registerUploadWorkout` | `internal/tools/workoutwrites.go` |
| `upload_workouts` | `registerUploadWorkouts` | `internal/tools/workoutwrites.go` |
| `update_workout` † | `registerUpdateWorkout` | `internal/tools/workoutwrites.go` |
| `schedule_workout` | `registerScheduleWorkout` | `internal/tools/workoutschedule.go` |
| `schedule_workouts` | `registerScheduleWorkouts` | `internal/tools/workoutschedule.go` |
| `schedule_week` | `registerScheduleWeek` | `internal/tools/scheduleweek.go` |
| `create_walk_run_workout` | `registerCreateWalkRunWorkout` | `internal/tools/builders_run.go` |
| `create_run_workout` | `registerCreateRunWorkout` | `internal/tools/builders_run.go` |
| `create_z2_walk_workout` | `registerCreateZ2WalkWorkout` | `internal/tools/builders_run.go` |
| `create_strength_workout` | `registerCreateStrengthWorkout` | `internal/tools/builders_strength.go` |
| `download_activity_file` | `registerDownloadActivityFile` | `internal/tools/downloads.go` |

### Destructive tier — 9 tools

| Tool | Go registrar | File |
| --- | --- | --- |
| `delete_custom_food` | `registerDeleteCustomFood` | `internal/tools/customfoodwrites.go` |
| `delete_food_log` | `registerDeleteFoodLog` | `internal/tools/foodlogwrites.go` |
| `delete_weigh_ins` | `registerDeleteWeighIns` | `internal/tools/weighindelete.go` |
| `delete_course` | `registerDeleteCourse` | `internal/tools/coursedelete.go` |
| `delete_activity` † | `registerDeleteActivity` | `internal/tools/activitylifecycle.go` |
| `delete_workout` | `registerDeleteWorkout` | `internal/tools/workoutdelete.go` |
| `delete_workouts` | `registerDeleteWorkouts` | `internal/tools/workoutdelete.go` |
| `unschedule_workout` | `registerUnscheduleWorkout` | `internal/tools/workoutschedule.go` |
| `unschedule_workouts` | `registerUnscheduleWorkouts` | `internal/tools/workoutschedule.go` |

† Not in the pinned manifest. See
[Tools beyond the pinned manifest](#tools-beyond-the-pinned-manifest).

‡ Registered in the **write** tier although the manifest classifies it
`external-side-effect`. See [Deliberate deviations](#deliberate-deviations).

### Tools beyond the pinned manifest

Five registered tools have **no record in `compat/tools.json` and must not get
one**: they are beyond the pinned upstream commit, so adding them to the manifest
would misreport the pinned surface. Four come from two open upstream pull
requests and the fifth from the `python-garminconnect` facade, which carries an
activity delete the pinned surface never exposes. They are additions, not parity,
and they are also entered in the ADR 0006 register. A contract snapshot test
cannot compare them with the manifest, so each is covered by a
documented-exclusion entry in `internal/tools/contract_test.go` instead.

| Tool | Tier | Beyond the pinned commit, from | What it does |
| --- | --- | --- | --- |
| `update_workout` | write | [Taxuspt/garmin_mcp#214](https://github.com/Taxuspt/garmin_mcp/pull/214) | Updates a workout in place. The body's `workoutId` is forced to the path id, so existing calendar schedules stay valid. |
| `get_exercise_types` | read-only | [Taxuspt/garmin_mcp#214](https://github.com/Taxuspt/garmin_mcp/pull/214) | Serves the strength exercise catalog Garmin publishes, read once at start-up, and the compiled-in subset when that read fails. The result names which one answered. |
| `set_activity_strength_exercise_sets` | write | [Taxuspt/garmin_mcp#208](https://github.com/Taxuspt/garmin_mcp/pull/208) | Replaces the exercise sets of a strength activity, then re-reads and compares them position by position. |
| `create_strength_training_activity` | write | [Taxuspt/garmin_mcp#208](https://github.com/Taxuspt/garmin_mcp/pull/208) | Creates a completed strength activity, replaces its sets, then re-reads the summary and checks the stored activity identifier. |
| `delete_activity` | destructive | `python-garminconnect` `delete_activity`; no upstream pull request | Deletes an activity. |

Both pull requests were open against `Taxuspt/garmin_mcp` when this matrix was
written: #214 "bump garminconnect to 0.3.7 and expose `update_workout` +
`get_exercise_types`", and #208 "add structured strength activity creation and
set updates". If either merges into a later pin, the tool moves from this section
into the manifest and its status becomes a normal `implemented` record.

## Deliberate deviations

Every entry below is a knowing difference between this server and the pinned
upstream contract. Each is mirrored in the ADR 0006 register and in
`docs/implementation-status.md`. A reader who assumes parity from a tool name
alone would be wrong about all of them.

### `download_activity_file` writes nothing to a server path

Upstream takes an `output_dir` argument, honours a `GARMIN_FIT_DOWNLOAD_DIR`
environment variable and a directory persisted by `set_fit_download_dir`, and
writes a file to the server filesystem.

This server implements **none of that**. The tool accepts an activity id and a
format and nothing else. No path is accepted from a caller, no environment
variable is read, no directory is persisted, and no file is opened. The bytes are
returned as a bounded embedded MCP resource under
`garmin://activity/{id}.{format}`, and a payload over the bound is refused rather
than truncated.

Two consequences a client must know:

- The manifest classifies the tool `external-side-effect` with the primary scope
  `local:files:write`. This server registers it in the **write** tier, so it is
  gated exactly like a Garmin write: operator enablement plus a granted write
  scope. There is no local filesystem scope on this surface.
- A caller that expected a path in the result gets content instead.

Reason: a remote tool must never be able to write an arbitrary server filesystem
path. Precedence rule: credential and tenant security above the pinned Taxuspt
contract.

### The `JWT_WEB` cookie fallback is not ported

`python-garminconnect` 0.3.10 recovers from a failed DI ticket exchange by
re-fetching the CAS service ticket through the web front end, scraping a
`JWT_WEB` cookie out of the session jar, and authenticating subsequent API calls
with that cookie instead of a bearer token (`Client._establish_session`,
`get_api_headers`).

This server does not. The reason is architectural, not effort: upstream is one
long-lived process where the fallback session and the next API call share a
single in-memory object, while every tool call here authenticates through
`Refresher.Do`, which reads the **persisted** per-principal DI token set. Upstream
itself never persists the cookie — `Client.dumps` serializes only the DI fields —
and its refresh path depends on the CAS ticket-granting cookie in that same
in-memory session. On stdio the `auth` command exits before `serve` starts, so
process memory cannot bridge them either.

A caller therefore sees a login failure where upstream might have recovered. That
is the accepted cost: a credential no later call can read is not a fallback, and a
Garmin session cookie is full account access, so carrying one for no reachable
benefit is the wrong trade.

Reason: credential lifecycle coherence above the pinned upstream behavior.
Reintroduction requires a durable credential lifecycle first.

### `set_fit_download_dir` is not registered

Its whole purpose is to persist a caller-supplied server filesystem path. It is
refused by design rather than stubbed, so a client discovers its absence at
`tools/list` instead of at call time.

### Scheduling has no duplicate avoidance, and says so

Upstream calls `_is_already_scheduled(workout_id, calendar_date)` before it
POSTs. That helper is a `workoutScheduleSummariesScalar` GraphQL calendar read.
This server now builds that request (`internal/garmin/api/calendar.go` over
`internal/garmin/client/graphql.go`), but only `schedule_week` runs the
pre-check. It is **not ported** into `schedule_workout` and `schedule_workouts`,
which are therefore honestly non-idempotent: calling one twice creates two
calendar entries.

Because upstream's own pre-check ends in a bare `except Exception: return False`,
it already fails open, so what is lost is best-effort de-duplication and not a
guarantee. See [Scheduling is not idempotent](#scheduling-is-not-idempotent).

The upstream docstrings for `schedule_workout` and `schedule_week` open with
`Idempotent:`, and those docstrings are the descriptions MCP clients receive.
**That sentence is absent from every description this server serves**, and a
registration test asserts that no description contains it. The descriptions say
the opposite instead — that repeating the call creates duplicate calendar
entries — because this is the text an agent reads when it decides whether a retry
is safe. The `idempotent` annotation hint is `false` for both tools.

### `schedule_week` reports its fail-open pre-check per item

It is registered, and it is the one scheduling tool that runs the calendar
pre-check. The check fails open exactly as upstream's does, so each item reports
`duplicate_check`: `checked` means the calendar answered, `failed` means nobody
could tell and the entry was sent anyway. The tool stays `non-idempotent`, and
its description says that repeating the call can create duplicates.

### `set_activity_description` cannot clear a description

An empty string is refused, at the tool layer and again in the API layer, where
`requireText` returns `client.ErrValidation`. Upstream accepts an empty write
field. A caller that wants to clear a description cannot do it through this
server.

### `upload_course` takes the GPX document itself, never a path

Upstream's `gpx_path` argument is an absolute filesystem path: courses.py opens
it and reads the file itself (courses.py:222-238). This server's `upload_course`
declares `gpx_content` instead, carrying the GPX document's own XML bytes as a
string, and there is no `gpx_path` argument anywhere on its schema.

This is the same rule `download_activity_file` and `set_fit_download_dir`
already follow, applied on the write side: a remote tool must never let a
caller name a server filesystem path. `gpx_path` on a network-reachable server
is a traversal and disclosure surface — a caller could ask this process to open
any file it can read — and this project accepts no caller-supplied filesystem
path anywhere, upload or download, by design (see AGENTS.md's file discipline).
The tool takes the document's own content because there is no other way to
receive a caller's GPX route without first accepting a path to fetch it from.

The rename is registered in `internal/tools/contract_test.go`'s
`schemaDeviations()`, the same mechanism `download_activity_file`'s `output_dir`
deviation uses, so the drift test does not fail on the renamed property or on
`gpx_content` being required where the manifest requires `gpx_path`.

### `add_body_composition` records the reading at UTC midnight, not the host's local midnight

Upstream's `FitEncoder.timestamp()` (fit.py:410-416) converts a naive
`datetime.fromisoformat(date)` through Python's `time.mktime`, which interprets
that naive value in whatever timezone the *Python process's host* carries —
never the Garmin account's own timezone, which `add_body_composition` has no
argument to receive in the first place. That host timezone is ambient state:
two deployments of the identical code, differing only in the host OS's zone
file, would silently record the same caller's reading on two different
calendar days, and a container host commonly runs UTC anyway, which would hide
the divergence until the code ran somewhere else.

This server fixes the instant at UTC midnight instead, deterministically,
regardless of where the process runs. The tradeoff is symmetric with upstream's
own bug in the other direction: an account whose real timezone is not UTC can
still see the reading attributed to the adjacent calendar day, because neither
this tool's manifest nor upstream's own wrapper takes a timestamp or timezone
argument that could resolve the account's real local date. Fixing that
correctly needs a manifest change (an explicit timestamp or timezone argument),
which is out of scope for a behavior-preserving parity port.

### `add_body_composition` reports the weight actually stored, not the caller's raw input

The FIT weight-scale field is a scaled `uint16` (scale 100, two decimal
places), and fit.py's own `FitBaseType.pack` (fit.py:178-183) truncates via
Python's `int(value)` rather than rounding. A caller who writes `70.006` kg
gets `70.00` stored, never `70.01`. This server's result echoes
`result.StoredWeightKG` — the value the FIT message actually carries after that
truncation — rather than the caller's own more-precise input, so a client
cannot be told a number was recorded that was not.

### `get_exercise_types` reads the published catalog, and keeps a compiled-in fallback

This server reads the catalog Garmin publishes at
`https://connect.garmin.com/web-data/exercises/Exercises.json` once, at server
start-up, and serves that immutable snapshot for the process lifetime. The
compiled-in **documented subset of the FIT `exercise_category` enum** remains, as
the fallback that answers whenever that read fails. Every result names which one
answered, in a `source` field: `garmin_web_catalog` or `built_in_subset`.

Why the published document rather than the vendored FIT profile: the two sets are
not the same and neither contains the other. The published catalog carries values
Garmin's own web application writes that the FIT enum cannot express — the bare
category-name entries such as `BENCH_PRESS` under `BENCH_PRESS`, and names with a
leading digit such as `_3_WAY_CALF_RAISE` — so an enum-only catalog would refuse
valid exercises. It also carries the muscle groups of every exercise, which the
enum has no equivalent for; they are surfaced per exercise and are absent for the
fallback.

The read is narrow by construction:

- **One compiled-in URL.** No configuration and no caller contributes a host, a
  path or a query, so the exception cannot be widened into a general fetcher.
- **Anonymous, on a dedicated client.** Its own `http.Transport` and connection
  pool, never `http.DefaultTransport`, no cookie jar, and no contact with the
  authenticated client, the token store or the refresher. Compression is disabled
  so the transport adds no header of its own: the request carries exactly `Accept`
  and `User-Agent`, both compiled in, and a test pins that whole header set rather
  than only the absence of a credential.
- **No redirect.** The 3xx is handed back unfollowed and refused by the status
  check, so the read cannot be moved to another host.
- **Bounded twice, and bounded while reading.** The response body is capped, and
  — because a byte cap does not bound what a document expands into — so are the
  categories, the exercises, the muscle lists and the rendered result. The
  document is walked as a stream, categories, exercises **and muscle arrays
  alike**, so each bound is applied at the key or element that crosses it and
  nothing beyond the accepted structure is ever held. One rule everywhere: over a
  bound the document is **refused, never truncated**, so a low-memory deployment
  falls back instead of dying at start-up and a part-served catalog can never
  disagree with what Garmin published.

  Every bound is set from the published document as measured on 2026-08-16, not
  invented. `MaxCatalogMuscles` was the exception — it was set to 8 without being
  measured, Garmin publishes 10, and under the refuse-never-trim rule that made a
  running server fall back to the 98-exercise subset until the live drift detector
  caught it:

  | Bound | Value | Observed in the published document | Headroom |
  | --- | --- | --- | --- |
  | Response body | 4 MiB | 198,082 bytes | 21.2x |
  | Categories | 256 | 47 | 5.4x |
  | Exercises per category | 1024 | 131 (`PLANK`) | 7.8x |
  | Exercises in total | 8192 | 1510 | 5.4x |
  | Muscle groups per list | 64 | 10 (`TOTAL_BODY`/`MAN_MAKERS`), from a vocabulary of 18 distinct groups | 6.4x over the longest list, 3.6x over the entire vocabulary |
  | Rendered result | 2 MiB | 225,666 bytes | 9.3x |

  The muscle bound is the loosest relative to observation on purpose: a muscle key
  is bounded at 64 bytes, so even a full list costs about 4 KB, and the rendered
  cap is the backstop that actually limits what a caller receives. The tightest is
  the rendered cap at 9.3x, which Garmin would have to grow the catalog nine-fold
  to cross. `TestAMuscleListAtGarminsObservedMaximumLoads` carries the observed
  muscle shape offline, so a bound set below reality fails in CI rather than in
  production — the live drift detector needs an account and an acknowledgement and
  cannot protect CI.

- **Unambiguous structure.** Two raw keys that normalize to one (`SQUAT` and
  ` squat `, and equally `""` and `"   "`, which are recorded before they are
  judged) are refused rather than resolved, because resolving them would depend on
  the order the document happens to carry them in. A structural member carried
  twice — two `categories` blocks, or two `exercises` blocks in one category — is
  refused for the same reason: the second block starts a fresh collision set, so a
  key could appear in both and the later one would silently win. Data after the
  top-level document is refused rather than ignored, so a recognized prefix
  followed by a second value or by garbage is never served.
- **Recognizable as Garmin's taxonomy.** A count-only gate would admit a
  fabricated document of invented categories, and those categories would then
  become the closed set that strength writes validate against. A fetched document
  must therefore carry every compiled-in category except the FIT `UNKNOWN`
  sentinel, and reproduce at least **half** of the compiled-in exercise names
  under their own parent. Measured on 2026-08-16: 33 of 33 required categories,
  and 63 of 98 names (64.3%). The floor tolerates **14 of those 63 names
  disappearing** before a legitimate document would be refused — Garmin renaming
  or dropping a fifth of the names this project compiled in. This is recognition,
  not authentication: the trust anchor is the TLS connection to
  `connect.garmin.com`. What it buys is that a document which is not the catalog
  cannot replace the compiled-in subset.

No failure can stop a server from starting.

The validation is asymmetric on purpose: the **category** is checked against a
closed set and an unknown category is refused, while an **exercise name** gets a
lexical check only — upper-case ASCII, digits and underscore, bounded in length.
Garmin remains authoritative for names, so a name this server does not list is
passed through rather than rejected. The fetched catalog is merged over the
compiled-in one, so the fetch can only widen what validates, never narrow it.

### `decoupling_percent` carries the opposite sign to upstream's `hr_drift_pct`

`get_activity_fit_data` reports `decoupling_percent` as

```
(first_half_ratio - second_half_ratio) / first_half_ratio * 100
```

where each ratio is that half's average power over its average heart rate. That
is the standard aerobic decoupling convention:

- **positive** means decoupling — the power-to-heart-rate ratio fell, so heart
  rate drifted **up** relative to power;
- **negative** means the ratio rose between the halves.

The upstream Python server computes the **inverse** difference, labels the result
`hr_drift_pct`, and annotates it "Negative drift = HR increased vs power
(decoupling)". For one and the same file the two servers therefore report the
same magnitude with opposite signs.

This server keeps its own sign, and upstream's label is the odd one: "drift" and
"decoupling" both name heart rate rising against power, and upstream reports that
as a negative number.

A worked example, with invented figures, shows why the sign here is the one that
matches its own ratios. Take a file whose power-to-heart-rate ratio is 2.300 over
the first half and 2.310 over the second. The ratio **rose**, so heart rate did
not drift up against power. This server computes `(2.300 - 2.310) / 2.300`, a
negative number, and negative here means the ratio rose. Upstream computes
the same magnitude with the opposite sign and calls a negative number decoupling,
which would describe that same file as drifting. The two half ratios travel in
the result precisely so the direction never has to be inferred from the sign.

The convention is stated in the `api.FITDrift` doc comment and in the
`decoupling_percent` schema description, and both name upstream's opposite sign.
**Do not flip it to match upstream.** A client that consumes both servers has to
normalize on its own, and it always can: `first_half_power_per_beat` and
`second_half_power_per_beat` are both in the result, so the direction is
recoverable from the ratios rather than from the sign alone.

No interpretation label is served. Upstream adds one (`well_coupled`); this
server does not. The threshold that would separate a coupled effort from a
decoupled one is not published by upstream, is not in any Garmin document, and is
not a number this project can source, so a label would be an invented cut-off
served as a finding. The three figures a label would be derived from are all in
the result, so a caller that has a threshold can apply its own.

The same rule governs the wording. The schema description states which way the
ratio moved and stops there; it does not call a negative figure well coupled,
because a description that grades the result is the unsourced threshold again in
prose. `TestDriftDescriptionStatesDirectionWithoutGradingIt` holds that line.

### `get_activities` returns three figures the manifest does not pin

`steps`, `elevation_gain_meters` and `elevation_loss_meters` are on each entry of
the `get_activities` and `get_activities_by_date` results. Upstream returns the
same three per listed activity. The manifest record for `get_activities` pins the
input schema only — its `outputShape` is `json-text` with an empty
`staticTopLevelKeys` — so the naming follows the field naming this server already
uses in the list result (`distance_meters`), not upstream's.

All three are omitted when the activity does not carry them: a swim counts no
steps, and an indoor ride records no altitude, so a zero would be a wrong reading
rather than a missing one.

### `get_activity_fit_data` reports descent and peak cadence

`descent_meters` and `max_cadence` sit beside `ascent_meters` and
`average_cadence` on every session, lap and whole-activity segment, which is what
upstream reports as `total_descent_m` and `max_cadence_rpm`. Both come from the
FIT profile — session `total_descent` (field 23) and `max_cadence` (field 19),
lap 22 and 18 — by the same route as ascent and average cadence, and both fall
back to a record-derived value only where the file carries no summary. The
derived descent is the derived ascent's own walk in the other direction at the
same noise threshold, so the two figures cannot disagree about what counts as a
move rather than as barometric jitter.

Ascent and descent are **absent**, not zero, when the file carries no altitude
series at all. Flat terrain and an activity recorded without a barometer are
different facts, and a zero would report the second as the first. A stream that
did carry altitude and did not move still reports a measured `0`.

### The FIT cadence keys name no unit, and upstream's do

Every cadence key in the `get_activity_fit_data` result is unit-free:
`average_cadence`, `max_cadence` and, on the per-second series, `cadence`.
Upstream spells them `average_cadence_rpm` and `max_cadence_rpm`, and **that
suffix is wrong for every run**. The FIT profile makes `avg_cadence` and
`max_cadence` dynamic fields: on a running session they are
`avg_running_cadence` and `max_running_cadence` in strides per minute, and only
on other sports are they rpm. The evidence is the SDK's own generated profile —
`mesgdef/session_gen.go` `GetAvgCadence`/`GetMaxCadence` and the same pair in
`lap_gen.go`.

Matching upstream is not a defence here, because a key that states a unit is a
claim a caller may convert on, and this one is off by roughly a factor of two on
a run. So the unit moved into the description, where it can name the sport it
depends on, and the key stopped asserting it.

This applies to `average_cadence` as much as to the newer `max_cadence`: the
average shipped first with the same wrong suffix, and both were corrected in one
change rather than leaving a result carrying two spellings of one quantity. The
per-second series and the gear-change events dropped the suffix too, so a reader
never meets two conventions in one document.

**Only the session and lap fields are sport-dependent, and the descriptions say
so per surface.** `Record.Cadence` has no dynamic form at all — the SDK declares
it `Units: rpm` and generates no `GetCadence` — so every figure derived from the
record stream is rpm whatever the sport. That splits the surfaces:

| Surface | Source | Description says |
| --- | --- | --- |
| segment (session, lap, whole activity) | prefers the session or lap summary | rpm, or strides/min running |
| climb, grade band | derived from the record stream | rpm |
| per-second series, gear-change event | the record field itself | rpm |

Describing a climb as sport-dependent would be as wrong as the old `_rpm`
suffix, in the other direction. `TestCadenceDescriptionsMatchTheirOwnField`
fails on either mistake.

`compat/tools.json` pins no result key for this tool, so nothing in the manifest
is loosened by the rename. `TestCadenceKeysNameNoUnit` and
`TestCadenceDescriptionsMatchTheirOwnField` keep both halves honest.

### The whole-activity FIT summary refuses a fold over a subset of sessions

A multisport file has one device summary per session. When the sessions disagree
in provenance — one carries `total_descent`, the next does not — the folded
figure is **absent** rather than a sum over the sessions that happened to carry
one. Absence hands the whole-activity figure back to the record-derived value,
which covers every sample of every session and is therefore complete.

The reasoning differs by figure and reaches the same rule:

- a **total** folded over a subset under-reports, and nothing in the result says
  a session is missing from it;
- a **peak** folded over a subset is a lower bound printed as a maximum;
- an **average** folded over a subset describes those sessions, not the activity.

Ascent and descent are held to this identically, because a file where one came
from the device and the other from the samples would invite exactly the
comparison neither figure supports. A single-session file — every ordinary
activity — is unaffected: it reproduces that session's figures exactly.

Absence is only safe where something answers in its place, so every folded field
was checked against `withProfileFigures` one at a time:

| Folded figure | What answers when the fold is absent |
| --- | --- |
| `total_elapsed_time` | the segment window, end minus start over the records |
| `total_distance` | the odometer delta across the record stream |
| `total_ascent`, `total_descent` | the elevation walk over the record altitudes |
| `avg_power`, `max_power`, `normalized_power` | the record power series |
| `avg_cadence`, `max_cadence` | the record cadence series |
| `avg_heart_rate`, `max_heart_rate` | the record heart-rate series |
| `total_calories` | **nothing** |

Calories is the exception, and it is the one field where absence is terminal
rather than a handoff: a FIT record carries no calorie field, so there is no
series to fall back to. It is still the honest answer. The alternatives are a
sum over the sessions that happened to report one — a total that silently covers
part of the activity — or a figure derived from power, which would be invented.
Nothing is lost either way — *unless the session list was itself truncated*: each
session's own calorie figure stays in `sessions[]` in the same result, so a caller
can add them up while seeing exactly which sessions reported none. That escape
hatch holds only while `sessions_truncated` is false. When it is true the list is
a subset, the sessions beyond the bound are not in the result at all, and the
whole-activity calorie figure is simply unavailable. The flag is what tells the
two cases apart, and it is why the flag is reported separately from
`samples_truncated`.

### A truncated FIT decode reports absence, not a prefix

The decode is bounded — `DefaultMaxFITRecords` samples, `DefaultMaxFITSessions`
sessions, `DefaultMaxFITLaps` laps — so a file past a bound is retained in part.
The analysis treats a part as a part, and each bound voids only what it actually
touched:

| Flag | What it means | What it voids |
| --- | --- | --- |
| `samples_truncated` | the record stream is a prefix | every figure *derived* from the records, and the whole-stream aggregates: power duration curve, grade bands, temperature split, decoupling |
| `sessions_truncated` | the session list is a subset | the whole-activity fold, which would otherwise total a subset |
| `laps_truncated` | the lap list is a subset | the lap list only |

A derived total summed over a prefix under-counts, a peak over a prefix is a
lower bound, and an average over a prefix may be representative or may not be —
and nothing in the number says which. So they are left absent rather than
reported over the stretch that survived.

**Lap truncation voids nothing but the lap list.** A file whose sessions are
whole and whose laps merely exceeded the cap keeps its whole-activity device
figures, because the watch computed those over the sessions and the lap bound
never touched them. Throwing them away would be the mirror of the defect this
section exists to prevent: there a partial figure was served as whole, here a
whole figure would be discarded because something unrelated was partial.

Two things are deliberately *kept* when a bound is hit. **Device figures** are
untouched: a session's `total_distance` was computed by the watch over the whole
segment before this server saw the file, so a cut in the samples cannot make it
partial. And **lists of detected events** — climbs, gear changes, the per-second
series itself — are returned with their own truncation flag, because each entry
is true of itself and the flag already says the list is not exhaustive. The line
is between a figure that claims to describe the whole and an entry that claims
only itself.

The suppression is scoped to what the bound actually cut. A lap that ended before
the last retained sample was measured in full and keeps its derived figures; only
a span reaching past the cut loses them.

`end_time` and `duration_seconds` follow the same rule as the figures. A
suppressed segment takes its window from the span the file declared, never from
the retained records: the last retained sample is where the bound fell, not where
the segment ended. A whole-activity summary with no declared window therefore
reports neither, rather than reporting the prefix's end as the ride's. `samples`
stays either way — it counts what was analysed and says so.

`get_power_duration_curve` follows the same rule from the other end: an activity
whose file hit the sample bound is counted in `activities_skipped` rather than
folded into the season bests, because a best folded from lower bounds is not a
best.

### `get_workout_by_id` serves the numeric identifier only

The UUID form that adaptive Garmin Coach plans use is not served. The input
schema accepts the numeric identifier, and the description says so.

### `get_lactate_threshold` returns the heart-rate series, which upstream drops

In the range branch this server returns `heart_rate_history` beside
`speed_history`. Upstream returns only the speed series, on every account and
every window, because of a key mismatch between the two pinned projects:

- `python-garminconnect` 0.3.10 reads both range endpoints and returns
  `{"speed": ..., "heart_rate": ..., "power": ...}`.
- `garmin_mcp` reads that result with `threshold.get("heartRate", [])`, which
  never matches the `heart_rate` key it was given, so the guard beneath it sees an
  empty list and omits the field.

Nothing signals the loss: no exception, no empty-list field, just an answer with
one fewer series than the account has.

A differential run over one real account and one 31-day window found it. Both
servers agreed exactly on the two speed readings; this server additionally
reported the two heart-rate readings, on the same dates, that upstream's own
client had already fetched and its tool layer then discarded.

This is a bug fixed, not a compatibility break, and it is recorded here rather
than in the ADR 0006 register for that reason. The endpoint set is unchanged: both
projects request the same two range paths.

### Training status codes are rendered as strings, where upstream passes the number through

`get_training_status` reports `training_status` and `fitness_trend` as strings. A
differential run against one account showed Garmin sending both as numbers there, and
upstream passing them through as numbers, while the feedback phrases beside them in
the same document are strings.

Garmin sends these fields as either type depending on the account and the device, so
the model decodes them through the tolerant union and renders one stable type rather
than whichever type arrived. The code itself is unchanged — `7` becomes `"7"`.

The cost is a caller that compares to a number, which works against upstream and
fails here. The benefit is that a caller comparing to a string works whatever Garmin
sent, instead of working until the day the account's device changes. Neither field is
an enumeration this server closes: an unrecognized code passes through intact.

### `get_training_status` describes one device, never two

Garmin's aggregated training-status document keys three blocks by device: the latest
status, the VO2 max section, and the monthly load balance. Upstream takes "the first
device" out of a Python dict for each block independently, which on a multi-device
account can answer with one watch's training status beside another watch's monthly
load, and can differ between two identical calls because dict order is not a promise.

This server picks the device once, in `api.SelectStatusDevice`, and every block
describes that device. The rule is: the device Garmin marks primary, then the most
recently dated entry, then the lowest key. When the chosen device reports no load
balance, the load figures are omitted rather than filled from another device — some
device's load is not this device's load. `status_devices_reported` and
`balance_devices_reported` still report how many devices the day carried, so a caller
can see that a choice was made.

### `get_lactate_threshold` refuses a lone date, where upstream answers anyway

Upstream takes `start_date` and `end_date` and, when only one is given, silently
answers with the account's latest reading instead of the window that was asked
for. This server refuses that call and names the missing argument.

The reason is that the silent form answers a different question from the one the
caller asked. A caller who sends `start_date` alone is asking about a window; an
answer carrying the latest reading is not a subset of that window, and nothing in
the result says so. A caller who wants the latest reading can omit both dates,
which is the documented way to ask for it and which this server does serve.

So the strictness is only at the boundary: both dates, or neither.

### What stays unregistered, and why

The two calendar reads that once sat here — `get_scheduled_workouts` and
`get_training_plan_workouts` — are registered now that the API layer builds the
GraphQL calendar request, and `get_activity_fit_data` followed once FIT decoding
landed in `internal/garmin/api`. That decoding now reads the official FIT profile
through `github.com/muktihari/fit`, so the session and lap summary figures a
device computes itself — distance, elapsed time, ascent, calories, average and
peak heart rate, average and peak power, normalized power — are reported as the
device wrote them rather than re-derived from the record stream. Coordinates are
still never retained and never returned: the SDK decodes them, this server reads
none of them, and the reused decode buffer is scrubbed of them after every
sample. See [ADR 0007](adr/0007-fit-decoding-library.md). What is
left is short, and it
is the whole gap between the implemented rows of [Tools](#tools) and the
deviations above:

| Tool | Status | Why |
| --- | --- | --- |
| `set_fit_download_dir` | not-implemented | Would persist a caller-supplied server filesystem path. Refused by design, not merely unbuilt. |
| `get_workout_by_id`, UUID form | **implemented**, numeric identifier only | The UUID form adaptive Garmin Coach plans use needs the Garmin Coach surface this server does not implement. The tool itself is registered. |

Leaving `set_fit_download_dir` unregistered is deliberate, and it is a refusal
rather than a gap: its only purpose is to persist a caller-supplied server
filesystem path. A stub that returned an error would occupy the upstream name
while proving nothing, and `docs/implementation-status.md` forbids counting a
placeholder as parity. `internal/tools/contract_test.go` asserts the name is not
registered, and `internal/tools/manifest_status_test.go` asserts it keeps the
`not-implemented` status in `compat/tools.json`.

## Resources

| URI | Status | Effect | Sensitivity | Scope | Upstream | Summary |
| --- | --- | --- | --- | --- | --- | --- |
| `workout://templates/simple-run` | **implemented** | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:303 | Simple run workout template (warmup, run, cooldown) |
| `workout://templates/interval-running` | **implemented** | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:312 | Interval running workout template with repeat groups |
| `workout://templates/tempo-run` | **implemented** | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:321 | Tempo run workout template with heart rate zone target |
| `workout://templates/strength-circuit` | **implemented** | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:330 | Strength training circuit template |
| `workout://reference/structure` | **implemented** | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:339 | Reference guide for workout JSON structure |

### The five resources are served, with two deliberate differences

All five live in `internal/resources`, are compiled in, and reach no Garmin
endpoint. They are registered through `mcpserver.AddResource` rather than as tools,
which is why they carry no tier: a resource that varied by principal or read Garmin
would be a tool, and the tier gate, the confirmation gate and the rate limiter all
key off tools deliberately. On the remote transport the HTTP layer authenticates
every request before dispatch, so a resource read still needs a verified bearer
token.

**The template contents are this server's, not upstream's byte-for-byte.** The URIs,
names, descriptions and `text/plain` media type match the manifest exactly, and the
step, condition, target and sport vocabularies were read from the pinned upstream's
own structure reference — they agree with the constants `internal/tools` builds
workouts from. The step counts, durations, distances and descriptions inside each
template were written here. A caller who expects a byte-identical document to
upstream's will not get one; a caller who expects a valid Garmin workout will.

**Each template is checked against this server's own upload path.**
`TestEveryTemplateIsAValidWorkoutDocument` parses every template through
`api.ParseWorkoutDocument`, and `TestEveryTemplateCarriesTheEnvelopeGarminExpects`
asserts the one-segment envelope and that no `stepOrder` repeats across a document,
nested repeat groups included. Upstream publishes its templates without that check.
A template the server that ships it would reject is worse than no template: the
caller follows the example and reads the failure as their own mistake.

The structure reference is generated from the same constants the templates use, so
the two cannot drift; `TestTheReferenceDescribesTheVocabularyTheTemplatesUse` fails
if a template uses a value the reference does not list.

## Extraction method

The inventory is produced by **static AST analysis**. Upstream Python is parsed, never executed, and
Python stays a development-time tool only. It is not a runtime, build, or test dependency of this server.

For each function decorated with `@app.tool()` under `src/garmin_mcp`, the extractor records:

- the registered tool name, which is the Python function name because no registration overrides it at this commit;
- source module and line, plus an immutable permalink at the pinned commit;
- the full docstring, which is the MCP tool description upstream exposes;
- a JSON Schema built from the signature: parameter names, JSON types mapped from annotations,
  `Optional[...]` / `X | None` nullability, `Literal[...]` enums, `Union[...]` as `anyOf`, literal defaults,
  and the required list, which holds the parameters without defaults;
- the output shape. All tools return a text content block, 136 of 138 serialise a JSON document with
  `json.dumps`, and static top-level keys of that document are recorded where they are literals;
- the `python-garminconnect` facade methods and raw HTTP calls the tool reaches, resolved through
  same-module helper functions up to three levels deep, with endpoint path literals and f-string templates;
- classification: `sensitivity` / `sensitivityTags`, `effect` / `secondaryEffects`, `scope` /
  `scopesRequired`, and `idempotency`.

Classification comes from the resolved HTTP verb and the endpoint semantics, not from the tool name.
`GET` reads and GraphQL queries are `read-only`, `PUT` and `POST` state changes are `write`, `DELETE`-backed
`delete_*` and `unschedule_*` flows are `destructive`, and local filesystem work is `external-side-effect`.
Scope is then derived from the effect class and the sensitivity domains, never from the tool name.
HTTP verbs for facade methods were confirmed against pinned
[`python-garminconnect@414b54023a31`](https://github.com/cyberjunky/python-garminconnect/commit/414b54023a31259232744bb67f00a2aa71065e09)
(0.3.10, `garminconnect/__init__.py`), which is a later release than the
`garminconnect==0.3.2` pin declared in the upstream `pyproject.toml`. This reference moved from 0.3.8 to
0.3.10 on 2026-08-14. Every facade method the pinned Taxuspt surface reaches still
exists at 0.3.10 with an identical verb set, so the re-pin changed no classification here.
The reconciliation window a Go port must close is therefore `0.3.2` through `0.3.10`,
not through 0.3.8.

### Known limits of static extraction

- Upstream tools return `str`, so there is no MCP `outputSchema` to extract. Response bodies come from an
  undocumented private API. Recorded output shapes are the statically visible envelope keys, not a
  validated schema of Garmin payloads.
- `set_fit_download_dir` makes no Garmin call at all. It is filesystem configuration only, so it is the one
  tool with `authRequired: false`. It still carries `mcpAuthorizationRequired: true`: it must not be
  invokable without an authorized caller holding `local:files:write`.
- 2 tools make no direct client call and reach Garmin only through a module-level helper (`query_garmin_graphql`): `get_garmin_coach_workouts`, `get_training_plan_workouts`.
  Their methods and endpoints come from helper resolution, so `directCalls` is empty while
  `garminconnectMethods` is not.
- 10 tools bypass the `python-garminconnect` facade and issue a raw request through the
  client (`garmin_client.client.put`/`post`/`delete`), so there is no facade method to port: `create_custom_food`, `delete_course`, `delete_custom_food`, `delete_food_log`, `get_courses`, `schedule_week`, `set_activity_description`, `set_activity_feel`, `set_perceived_effort`, `upload_course`.
  For these, the endpoint literal in `endpoints` is the contract.
- Endpoint values shown as `<garmin_connect_*>` are attribute references to base-URL constants on the
  `garminconnect` client, not inline literals. Resolve them in `python-garminconnect` when implementing.
- `get_scheduled_workouts`, `get_garmin_coach_workouts`, and `get_training_plan_workouts` go through
  `query_garmin_graphql`, so their `httpMethodHints` include `POST`. They are still `read-only`: the POST
  carries a GraphQL query and changes nothing. Do not derive the effect class from the verb alone here.
- Idempotency for a private, undocumented API is a documented judgement, not a guarantee. `DELETE`-backed
  tools are marked `idempotent` because the end state converges, even though a repeat call reports a
  missing target.
- `update_custom_food` is `idempotent` on its PUT target (a fixed `food_id`, absolute values, no
  duplicates) but its preservation of caller-omitted nutrient fields is a best-effort pre-fetch wrapped
  in `except Exception: pass`, which also misses silently when `food_name` no longer matches the stored
  record. When that pre-fetch fails open, omitted fields are dropped from the payload instead of carried
  forward, so a replay can clear values the first call preserved. Retry it only with the complete field
  set. This is the one other fail-open path found in the manifest; unlike scheduling it cannot create a
  duplicate object, so the classification stays `idempotent` with the caveat recorded in
  `idempotencyNote`.
- Three further `except Exception: pass` sites (the activity weight lookup in `activity_analysis.py`, and
  the per-day loops behind `get_hrv_trend` and `get_respiration_trend`) fail open on response
  completeness only. They belong to `read-only` tools that write nothing, so they cannot affect an
  idempotency claim.

### Scheduling is not idempotent

`schedule_workout`, `schedule_workouts`, and `schedule_week` are **`non-idempotent`**, and an earlier
revision of this matrix was wrong to call them idempotent.

Each calls `_is_already_scheduled(workout_id, calendar_date)` before POSTing. That helper runs a
`workoutScheduleSummariesScalar` GraphQL query bounded to the single date and returns `True` only when
an entry matches both `workoutId` and `scheduleDate` exactly; on a match the POST is skipped and success
is reported. The helper ends in a bare `except Exception: return False`, so any failure of the check
itself — transport or auth error, a rejected `calendar_date`, or an undocumented change to the GraphQL
payload shape — is reported as "not scheduled", and upstream deliberately falls through to the bare
`workout-service/schedule/{workout_id}` POST, which creates a second calendar entry for the same workout
and date.

De-duplication that fails open is best-effort, not a guarantee, so a retry can create a duplicate. For
`schedule_workouts` and `schedule_week` the fail-open path is per item, so one replay can duplicate
several days at once, and `schedule_workouts` items carrying inline `workout_data` upload a workout
template through `upload_workout` before the pre-check runs — an upload that is not deduplicated at all,
so a replay adds another library template even when the calendar entry is correctly skipped.

**Retry policy: never retry these three automatically, and never treat them as safe to replay.** On a
failed, partial, or ambiguous result, re-read the calendar with `get_scheduled_workouts`, let the caller
decide, and remove any duplicate with `unschedule_workout`.

Only a server-side idempotency key, or a pre-check whose own failure aborts instead of falling
through, would make scheduling actually idempotent.

**What this server does.** `schedule_workout` and `schedule_workouts` do not port the pre-check, so
they carry no duplicate avoidance whatsoever and say so in their descriptions and in their
`idempotent: false` annotation. `schedule_week` is registered and does run the pre-check, and it
reports per item whether the check answered or failed open. All three stay `non-idempotent`.
This is a deliberate deviation and it is recorded in
[Deliberate deviations](#deliberate-deviations) and in the ADR 0006 register.

One consequence to carry forward: the upstream docstrings for `schedule_workout` and `schedule_week`
open with "Idempotent:", and those docstrings are the tool descriptions MCP clients receive. The
`description` field in `compat/tools.json` is a verbatim copy, so for these two records `description`
and `idempotency` deliberately disagree. The classification is correct and the upstream description is
not. That sentence is **absent from every description this server serves**, and a registration test
asserts that no description contains it, because it is the text an agent reads when deciding whether a
retry is safe.

### Regenerating

```sh
SCRATCH=$(mktemp -d)
curl -sSL https://codeload.github.com/Taxuspt/garmin_mcp/tar.gz/3610be6feed93088d85b0f35aba9d7d07c2505a7 \
  | tar xz -C "$SCRATCH"
curl -sSL -o "$SCRATCH/gc_init.py" https://raw.githubusercontent.com/cyberjunky/python-garminconnect/414b54023a31259232744bb67f00a2aa71065e09/garminconnect/__init__.py
python3 extract.py \
  "$SCRATCH/garmin_mcp-3610be6feed93088d85b0f35aba9d7d07c2505a7/src/garmin_mcp" "$SCRATCH/raw.json"
python3 gen.py "$SCRATCH/raw.json" . "$SCRATCH/gc_init.py"
```

`extract.py` and `gen.py` are development-time throwaways. This phase deliberately commits only the three
artifacts (`compat/tools.json`, `compat/resources.json`, `docs/parity.md`), so the scripts are not in the
repository and the algorithm above is the reproducible specification. Upstream sources are not vendored:
they are fetched into a scratch directory and read there, and no upstream code is executed.

**On a committed regenerator.** This section used to say a **Go** regenerator was deferred to a later
phase. That over-specified the mechanism, and the obligation is narrowed rather than carried:

- A full Go re-implementation is **rejected**. Extraction is AST-level over Python source, so it needs
  either a third-party Go Python parser — a nontrivial dependency, and this repository requires a
  rationale, a licence entry and a notices entry for one — or hand-rolled pattern matching, which would
  silently emit wrong contracts the first time upstream reformats a decorator. CPython already owns that
  grammar.
- Committing the Python scripts has a prerequisite that is not met: a committed generator becomes CI's
  authority, so it must first reproduce the **reviewed** artifacts byte for byte. These manifests have
  been corrected by hand since they were generated — implementation status, classifications, deliberate
  deviations — so a generator that disagreed would invite "fixing" it by regenerating over content the
  137 contract tests depend on. Establishing that golden reproduction is the real work, and it is not
  done.

What **is** enforced today, offline and in the ordinary test job, is the coupling:
`TestUpstreamPinsAgreeBetweenTheDocAndBothManifests` (`internal/tools/manifest_pin_test.go`) fails when
this file's pin and the commit embedded in either manifest disagree. That is the failure a pin bump
actually has — the pin moves, the manifests keep describing the old commit, and every contract test
silently validates the server against a snapshot of something else. Regenerating the manifests stays the
manual procedure above.

Bump the pinned SHA only through the process in `docs/upstream-pins.md`, and record any deliberate
compatibility break in an ADR as well as in this matrix.
