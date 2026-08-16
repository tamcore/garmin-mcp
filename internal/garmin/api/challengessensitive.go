package api

import "log/slog"

// The challenges domain's LogValue implementations. Every model here carries a
// badge or goal figure, a date, or an identity field tied to a person, so each
// reports its shape only, following the same discipline sensitive.go documents
// for the rest of this package. Kept in a file of their own rather than folded
// into sensitive.go, the same reason nutritionsensitive.go gives.

// LogValue reports which parts of an earned badge arrived, never a name, a
// point count, an earned date, a progress figure or the identity fields
// Garmin may attach.
func (b EarnedBadge) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "earnedBadge"),
		slog.String("badgeName", presence(b.BadgeName.IsSet())),
		slog.String("badgePoints", presence(b.BadgePoints.IsSet())),
		slog.String("badgeEarnedDate", presence(b.BadgeEarnedDate.IsSet())),
		slog.String("badgeCategoryId", presence(b.BadgeCategoryID.IsSet())),
		slog.String("badgeDifficultyId", presence(b.BadgeDifficultyID.IsSet())),
		slog.String("badgeUnitId", presence(b.BadgeUnitID.IsSet())),
		slog.String("badgeProgressValue", presence(b.BadgeProgressValue.IsSet())),
		slog.String("badgeTargetValue", presence(b.BadgeTargetValue.IsSet())),
		slog.String("badgeStartDate", presence(b.BadgeStartDate.IsSet())),
		slog.String("badgeEndDate", presence(b.BadgeEndDate.IsSet())),
		slog.String("badgeAssocType", presence(b.BadgeAssocType.IsSet())),
		slog.String("badgeAssocDataId", presence(b.BadgeAssocDataID.IsSet())),
		slog.String("badgeSeriesId", presence(b.BadgeSeriesID.IsSet())),
		slog.String("userProfileId", presence(b.UserProfileID.IsSet())),
		slog.String("displayName", presence(b.DisplayName != nil)),
		slog.String("fullName", presence(b.FullName != nil)),
	)
}

// LogValue reports how many goals arrived and whether the walk was
// truncated, never a goal's own raw content: Goal carries no typed field at
// all (see its doc comment in challenges.go), so there is nothing to report
// a field's presence for, and the raw JSON itself must never reach a log
// line.
func (g GoalResult) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "goalResult"),
		slog.Int("goals", len(g.Goals)),
		slog.Bool("truncated", g.Truncated),
	)
}

// LogValue reports which predicted distances arrived, never a predicted time.
func (r RacePredictionSet) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "racePredictionSet"),
		slog.String("calendarDate", presence(r.CalendarDate != nil)),
		slog.String("time5K", presence(r.Time5K.IsSet())),
		slog.String("time10K", presence(r.Time10K.IsSet())),
		slog.String("timeHalfMarathon", presence(r.TimeHalfMarathon.IsSet())),
		slog.String("timeMarathon", presence(r.TimeMarathon.IsSet())),
		slog.Any("payload", r.raw),
	)
}

// LogValue reports which identity, points and progress fields arrived, never a
// name, a date or a progress figure.
func (b BadgeChallengeItem) LogValue() slog.Value {
	_, hasTitle := b.Title()
	return slog.GroupValue(
		slog.String("model", "badgeChallengeItem"),
		slog.String("uuid", presence(b.UUID.IsSet())),
		slog.String("name", presence(hasTitle)),
		slog.String("points", presence(b.Points.IsSet())),
		slog.String("categoryId", presence(b.CategoryID.IsSet())),
		slog.String("statusId", presence(b.StatusID.IsSet())),
		slog.String("unitId", presence(b.UnitID.IsSet())),
		slog.String("startDate", presence(b.StartDate.IsSet())),
		slog.String("endDate", presence(b.EndDate.IsSet())),
		slog.String("userJoined", presence(b.UserJoined != nil)),
		slog.String("earnedDate", presence(b.EarnedDate.IsSet())),
		slog.String("progressValue", presence(b.ProgressValue.IsSet())),
		slog.String("targetValue", presence(b.TargetValue.IsSet())),
		slog.String("joinable", presence(b.Joinable != nil)),
	)
}

// LogValue reports which identity and ranking fields arrived, never a name, a
// description or a date.
func (a AdhocChallengeItem) LogValue() slog.Value {
	_, hasTitle := a.Title()
	return slog.GroupValue(
		slog.String("model", "adhocChallengeItem"),
		slog.String("uuid", presence(a.UUID.IsSet())),
		slog.String("name", presence(hasTitle)),
		slog.String("description", presence(a.Description.IsSet())),
		slog.String("activityTypeId", presence(a.ActivityTypeID.IsSet())),
		slog.String("statusId", presence(a.StatusID.IsSet())),
		slog.String("startDate", presence(a.StartDate.IsSet())),
		slog.String("endDate", presence(a.EndDate.IsSet())),
		slog.String("userRanking", presence(a.UserRanking.IsSet())),
		slog.String("playerCount", presence(a.PlayerCount.IsSet())),
	)
}

// LogValue reports which identity and progress fields arrived, never a name, a
// date or a progress figure.
func (v VirtualChallengeItem) LogValue() slog.Value {
	_, hasTitle := v.Title()
	_, hasProgress := v.Progress()
	_, hasTarget := v.Target()
	return slog.GroupValue(
		slog.String("model", "virtualChallengeItem"),
		slog.String("uuid", presence(v.UUID.IsSet())),
		slog.String("name", presence(hasTitle)),
		slog.String("startDate", presence(v.StartDate.IsSet())),
		slog.String("endDate", presence(v.EndDate.IsSet())),
		slog.String("progress", presence(hasProgress)),
		slog.String("target", presence(hasTarget)),
		slog.String("unitId", presence(v.UnitID.IsSet())),
	)
}

// LogValue reports how many challenges the page carries and the shape of the
// retained payload, never a challenge's own fields: those are reported by each
// item's own LogValue, which slog reaches on its own when a caller logs one
// element directly.
func (p BadgeChallengePage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "badgeChallengePage"),
		slog.Int("challenges", len(p.Challenges)),
		slog.Any("payload", p.raw),
	)
}

// LogValue reports how many challenges the page carries and the shape of the
// retained payload; see BadgeChallengePage.LogValue.
func (p AdhocChallengePage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "adhocChallengePage"),
		slog.Int("challenges", len(p.Challenges)),
		slog.Any("payload", p.raw),
	)
}

// LogValue reports how many challenges the page carries and the shape of the
// retained payload; see BadgeChallengePage.LogValue.
func (p VirtualChallengePage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "virtualChallengePage"),
		slog.Int("challenges", len(p.Challenges)),
		slog.Any("payload", p.raw),
	)
}
