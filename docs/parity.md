# Upstream parity matrix

Authoritative compatibility inventory of the upstream Python MCP server, extracted on 2026-08-14
from [`Taxuspt/garmin_mcp@3610be6feed9`](https://github.com/Taxuspt/garmin_mcp/commit/3610be6feed93088d85b0f35aba9d7d07c2505a7).

Machine-readable source of truth: [`compat/tools.json`](../compat/tools.json) and
[`compat/resources.json`](../compat/resources.json). This document is the human-readable view of those files.

## Measured totals

- **138** `@app.tool()` registrations under `src/garmin_mcp`.
- **5** `@app.resource(...)` registrations, all in `src/garmin_mcp/workout_templates.py`.
- Every tool and resource is currently `not-implemented` in this Go server.

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
| idempotent | 121 |
| non-idempotent | 17 |

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
- The five resources carry no user data, but they are still gated on `garmin:workouts:read` so
  registration keeps one fail-closed path with no "no scope required" branch.

## Tools

This table is the tool-to-scope map. `Scope` is the primary gate; tools that need more than that
one scope are listed under [Tools requiring more than one scope](#tools-requiring-more-than-one-scope).

| Tool | Status | Effect | Sensitivity | Scope | Idempotency | Upstream |
| --- | --- | --- | --- | --- | --- | --- |
| `add_body_composition` | not-implemented | write | health | `garmin:health:write` | non-idempotent | data_management.py:22 |
| `add_gear_to_activity` | not-implemented | write | device | `garmin:devices:write` | idempotent | gear_management.py:157 |
| `add_hydration_data` | not-implemented | write | health | `garmin:health:write` | non-idempotent | data_management.py:98 |
| `add_weigh_in` | not-implemented | write | health | `garmin:health:write` | non-idempotent | weight_management.py:156 |
| `add_weigh_in_with_timestamps` | not-implemented | write | health | `garmin:health:write` | non-idempotent | weight_management.py:176 |
| `count_activities` | not-implemented | read-only | ordinary | `garmin:activities:read` | idempotent | activity_management.py:812 |
| `create_custom_food` | not-implemented | write | nutrition | `garmin:nutrition:write` | non-idempotent | nutrition.py:269 |
| `create_manual_activity` | not-implemented | write | health | `garmin:activities:write` | non-idempotent | activity_management.py:892 |
| `create_run_workout` | not-implemented | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:392 |
| `create_strength_workout` | not-implemented | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:484 |
| `create_walk_run_workout` | not-implemented | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:344 |
| `create_z2_walk_workout` | not-implemented | write | health | `garmin:workouts:write` | non-idempotent | workout_builders.py:447 |
| `delete_course` | not-implemented | destructive | location | `garmin:activities:destructive` | idempotent | courses.py:289 |
| `delete_custom_food` | not-implemented | destructive | nutrition | `garmin:nutrition:destructive` | idempotent | nutrition.py:518 |
| `delete_food_log` | not-implemented | destructive | nutrition | `garmin:nutrition:destructive` | idempotent | nutrition.py:720 |
| `delete_weigh_ins` | not-implemented | destructive | health | `garmin:health:destructive` | idempotent | weight_management.py:136 |
| `delete_workout` | not-implemented | destructive | health | `garmin:workouts:destructive` | idempotent | workouts.py:996 |
| `delete_workouts` | not-implemented | destructive | health | `garmin:workouts:destructive` | idempotent | workouts.py:1023 |
| `download_activity_file` | not-implemented | external-side-effect | location | `local:files:write` | idempotent | activity_analysis.py:1263 |
| `download_workout` | not-implemented | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:768 |
| `get_activities` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:830 |
| `get_activities_by_date` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:50 |
| `get_activities_fordate` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:161 |
| `get_activity` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:210 |
| `get_activity_exercise_sets` | not-implemented | read-only | health | `garmin:activities:read` | idempotent | activity_management.py:795 |
| `get_activity_fit_data` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_analysis.py:1053 |
| `get_activity_gear` | not-implemented | read-only | device | `garmin:devices:read` | idempotent | activity_management.py:778 |
| `get_activity_hr_in_timezones` | not-implemented | read-only | health | `garmin:activities:read` | idempotent | activity_management.py:741 |
| `get_activity_power_in_timezones` | not-implemented | read-only | health | `garmin:activities:read` | idempotent | activity_management.py:758 |
| `get_activity_split_summaries` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:638 |
| `get_activity_splits` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:539 |
| `get_activity_typed_splits` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:621 |
| `get_activity_types` | not-implemented | read-only | ordinary | `garmin:activities:read` | idempotent | activity_management.py:942 |
| `get_activity_weather` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_management.py:655 |
| `get_adhoc_challenges` | not-implemented | read-only | ordinary | `garmin:challenges:read` | idempotent | challenges.py:363 |
| `get_all_day_events` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:687 |
| `get_all_day_stress` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:671 |
| `get_available_badge_challenges` | not-implemented | read-only | health | `garmin:challenges:read` | idempotent | challenges.py:412 |
| `get_badge_challenges` | not-implemented | read-only | health | `garmin:challenges:read` | idempotent | challenges.py:445 |
| `get_blood_pressure` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:309 |
| `get_body_battery` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:247 |
| `get_body_battery_events` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:293 |
| `get_body_composition` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:115 |
| `get_courses` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | courses.py:173 |
| `get_custom_food_serving_units` | not-implemented | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:253 |
| `get_custom_foods` | not-implemented | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:220 |
| `get_cycling_ftp` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:647 |
| `get_daily_steps` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:172 |
| `get_daily_weigh_ins` | not-implemented | read-only | health | `garmin:health:read` | idempotent | weight_management.py:86 |
| `get_device_alarms` | not-implemented | read-only | device | `garmin:devices:read` | idempotent | devices.py:279 |
| `get_device_last_used` | not-implemented | read-only | device | `garmin:devices:read` | idempotent | devices.py:62 |
| `get_device_settings` | not-implemented | read-only | device | `garmin:devices:read` | idempotent | devices.py:95 |
| `get_device_solar_data` | not-implemented | read-only | device | `garmin:devices:read` | idempotent | devices.py:229 |
| `get_devices` | not-implemented | read-only | device | `garmin:devices:read` | idempotent | devices.py:23 |
| `get_earned_badges` | not-implemented | read-only | ordinary | `garmin:challenges:read` | idempotent | challenges.py:297 |
| `get_endurance_score` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:274 |
| `get_fitnessage_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:487 |
| `get_floors` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:326 |
| `get_full_name` | not-implemented | read-only | profile | `garmin:profile:read` | idempotent | user_profile.py:22 |
| `get_garmin_coach_workouts` | not-implemented | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:1098 |
| `get_gear` | not-implemented | read-only | device | `garmin:devices:read` | idempotent | gear_management.py:42 |
| `get_goals` | not-implemented | read-only | health | `garmin:health:read` | idempotent | challenges.py:237 |
| `get_heart_rates` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:358 |
| `get_heart_rates_summary` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:377 |
| `get_hill_score` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:217 |
| `get_hrv_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:431 |
| `get_hrv_trend` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:998 |
| `get_hydration_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:415 |
| `get_inprogress_virtual_challenges` | not-implemented | read-only | health | `garmin:challenges:read` | idempotent | challenges.py:552 |
| `get_lactate_threshold` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:675 |
| `get_lifestyle_logging_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:703 |
| `get_menstrual_calendar_data` | not-implemented | read-only | womens-health | `garmin:womens-health:read` | idempotent | womens_health.py:75 |
| `get_menstrual_data_for_date` | not-implemented | read-only | womens-health | `garmin:womens-health:read` | idempotent | womens_health.py:60 |
| `get_morning_training_readiness` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:867 |
| `get_non_completed_badge_challenges` | not-implemented | read-only | health | `garmin:challenges:read` | idempotent | challenges.py:478 |
| `get_nutrition_daily_food_log` | not-implemented | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:32 |
| `get_nutrition_daily_meals` | not-implemented | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:51 |
| `get_nutrition_daily_settings` | not-implemented | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:71 |
| `get_personal_record` | not-implemented | read-only | health | `garmin:health:read` | idempotent | challenges.py:252 |
| `get_power_duration_curve` | not-implemented | read-only | location | `garmin:activities:read` | idempotent | activity_analysis.py:1150 |
| `get_pregnancy_summary` | not-implemented | read-only | womens-health | `garmin:womens-health:read` | idempotent | womens_health.py:49 |
| `get_primary_training_device` | not-implemented | read-only | device | `garmin:devices:read` | idempotent | devices.py:177 |
| `get_progress_summary_between_dates` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:161 |
| `get_race_predictions` | not-implemented | read-only | health | `garmin:health:read` | idempotent | challenges.py:513 |
| `get_respiration_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:588 |
| `get_respiration_summary` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:607 |
| `get_respiration_trend` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:1227 |
| `get_rhr_day` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:342 |
| `get_scheduled_workouts` | not-implemented | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:1059 |
| `get_sleep_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:431 |
| `get_sleep_summary` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:450 |
| `get_spo2_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:636 |
| `get_stats` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:22 |
| `get_stats_and_body` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:137 |
| `get_steps_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:153 |
| `get_stress_data` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:524 |
| `get_stress_summary` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:543 |
| `get_training_effect` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:388 |
| `get_training_load_balance` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:899 |
| `get_training_load_trend` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:791 |
| `get_training_plan_workouts` | not-implemented | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:1132 |
| `get_training_readiness` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:189 |
| `get_training_status` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:566 |
| `get_unit_system` | not-implemented | read-only | profile | `garmin:profile:read` | idempotent | user_profile.py:31 |
| `get_user_profile` | not-implemented | read-only | profile | `garmin:profile:read` | idempotent | user_profile.py:40 |
| `get_user_summary` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:99 |
| `get_userprofile_settings` | not-implemented | read-only | profile | `garmin:profile:read` | idempotent | user_profile.py:51 |
| `get_vo2max_trend` | not-implemented | read-only | health | `garmin:health:read` | idempotent | training.py:1072 |
| `get_weekly_intensity_minutes` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:811 |
| `get_weekly_steps` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:722 |
| `get_weekly_stress` | not-implemented | read-only | health | `garmin:health:read` | idempotent | health_wellness.py:769 |
| `get_weigh_ins` | not-implemented | read-only | health | `garmin:health:read` | idempotent | weight_management.py:22 |
| `get_workout_by_id` | not-implemented | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:729 |
| `get_workouts` | not-implemented | read-only | health | `garmin:workouts:read` | idempotent | workouts.py:707 |
| `log_custom_food` | not-implemented | write | nutrition | `garmin:nutrition:write` | non-idempotent | nutrition.py:546 |
| `log_food` | not-implemented | write | nutrition | `garmin:nutrition:write` | non-idempotent | nutrition.py:637 |
| `remove_gear_from_activity` | not-implemented | write | device | `garmin:devices:write` | idempotent | gear_management.py:182 |
| `request_reload` | not-implemented | write | health | `garmin:health:write` | idempotent | training.py:778 |
| `schedule_week` | not-implemented | write | health | `garmin:workouts:write` | idempotent | workout_builders.py:522 |
| `schedule_workout` | not-implemented | write | health | `garmin:workouts:write` | idempotent | workouts.py:1154 |
| `schedule_workouts` | not-implemented | write | health | `garmin:workouts:write` | idempotent | workouts.py:1212 |
| `search_foods` | not-implemented | read-only | nutrition | `garmin:nutrition:read` | idempotent | nutrition.py:144 |
| `set_activity_description` | not-implemented | write | ordinary | `garmin:activities:write` | idempotent | activity_management.py:386 |
| `set_activity_event_type` | not-implemented | write | ordinary | `garmin:activities:write` | idempotent | activity_management.py:416 |
| `set_activity_feel` | not-implemented | write | health | `garmin:activities:write` | idempotent | activity_management.py:503 |
| `set_activity_name` | not-implemented | write | ordinary | `garmin:activities:write` | idempotent | activity_management.py:313 |
| `set_activity_type` | not-implemented | write | ordinary | `garmin:activities:write` | idempotent | activity_management.py:341 |
| `set_blood_pressure` | not-implemented | write | health | `garmin:health:write` | non-idempotent | data_management.py:75 |
| `set_fit_download_dir` | not-implemented | external-side-effect | ordinary | `local:files:write` | idempotent | activity_analysis.py:1363 |
| `set_nutrition_daily_settings` | not-implemented | write | nutrition | `garmin:nutrition:write` | idempotent | nutrition.py:90 |
| `set_perceived_effort` | not-implemented | write | health | `garmin:activities:write` | idempotent | activity_management.py:467 |
| `unschedule_workout` | not-implemented | destructive | health | `garmin:workouts:destructive` | idempotent | workouts.py:1345 |
| `unschedule_workouts` | not-implemented | destructive | health | `garmin:workouts:destructive` | idempotent | workouts.py:1381 |
| `update_custom_food` | not-implemented | write | nutrition | `garmin:nutrition:write` | idempotent | nutrition.py:376 |
| `upload_course` | not-implemented | write | location | `garmin:activities:write` | non-idempotent | courses.py:203 |
| `upload_workout` | not-implemented | write | health | `garmin:workouts:write` | non-idempotent | workouts.py:794 |
| `upload_workouts` | not-implemented | write | health | `garmin:workouts:write` | non-idempotent | workouts.py:933 |
| `upsert_and_log` | not-implemented | write | nutrition | `garmin:nutrition:write` | non-idempotent | nutrition.py:745 |

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

## Resources

| URI | Status | Effect | Sensitivity | Scope | Upstream | Summary |
| --- | --- | --- | --- | --- | --- | --- |
| `workout://templates/simple-run` | not-implemented | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:303 | Simple run workout template (warmup, run, cooldown) |
| `workout://templates/interval-running` | not-implemented | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:312 | Interval running workout template with repeat groups |
| `workout://templates/tempo-run` | not-implemented | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:321 | Tempo run workout template with heart rate zone target |
| `workout://templates/strength-circuit` | not-implemented | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:330 | Strength training circuit template |
| `workout://reference/structure` | not-implemented | read-only | ordinary | `garmin:workouts:read` | workout_templates.py:339 | Reference guide for workout JSON structure |

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
  tool with `authRequired: false`.
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
- `schedule_workout`, `schedule_workouts`, and `schedule_week` are `idempotent` because of the upstream MCP
  layer, not the endpoint: each pre-checks the calendar with `_is_already_scheduled()` (a
  `workoutScheduleSummariesScalar` GraphQL query) and skips the POST on a match. The bare
  `workout-service/schedule` POST is not idempotent, and upstream deliberately falls through to it when the
  pre-check itself raises. A Go port must keep the pre-check. `schedule_workouts` items that carry inline
  `workout_data` also upload a template first, and that upload is not deduplicated.

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
they are fetched into a scratch directory and read there, and no upstream code is executed. Committing a
Go regenerator, so that drift against a new upstream pin can be diffed in CI, is deferred to a later phase.

Bump the pinned SHA only through the process in `docs/upstream-pins.md`, and record any deliberate
compatibility break in an ADR as well as in this matrix.
