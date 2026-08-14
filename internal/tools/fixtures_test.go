package tools_test

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture here is synthetic. The health values are invented, the coordinates
// are the null island, and no fixture is a recording of a real account.
const (
	profileBody = `{"profileId":900001,"displayName":"` + testDisplayName + `",` +
		`"fullName":"Fake Tester","location":"Nowhere","userLevel":3}`

	settingsBody = `{"id":900001,"userData":{"measurementSystem":"metric","birthDate":"1990-01-01",` +
		`"weight":72000.0,"height":180.0,"timeFormat":"time_twenty_four_hr"}}`

	devicesBody = `[{"deviceId":3001,"unitId":"4001","serialNumber":"SYNTH-0001",` +
		`"productDisplayName":"Fake Forerunner","productSku":"010-00000-00"},` +
		`{"deviceId":3002,"serialNumber":"SYNTH-0002","productDisplayName":"Fake Edge"}]`

	sleepBody = `{"dailySleepDTO":{"id":5001,"calendarDate":"` + testCalendarDate + `",` +
		`"sleepTimeSeconds":27000,"deepSleepSeconds":5400,"lightSleepSeconds":16200,` +
		`"remSleepSeconds":5400,"awakeSleepSeconds":600,"averageRespirationValue":14.2,` +
		`"averageSpO2Value":96,"sleepQualityTypeName":"good"},"sleepLevels":[{"a":1}]}`

	summaryBody = `{"userProfileId":900001,"calendarDate":"` + testCalendarDate + `",` +
		`"totalSteps":9123,"totalDistanceMeters":7345,"totalKilocalories":2410,` +
		`"activeKilocalories":610,"restingHeartRate":52,"minHeartRate":48,"maxHeartRate":171,` +
		`"averageStressLevel":24,"bodyBatteryHighestValue":88,"bodyBatteryLowestValue":19,` +
		`"floorsAscended":11}`

	privacyProtectedSummaryBody = `{"calendarDate":"` + testCalendarDate + `","privacyProtected":true}`

	splitEntry = `{"type":"INTERVAL_ACTIVE","messageIndex":0,"distance":1000.0,"duration":240.5,` +
		`"averageHR":151,"maxHR":168,"calories":81,"maxElevation":12.5,` +
		`"startTimeGMT":"2026-01-31T06:12:00.0"}`

	exerciseSetsBody = `{"exerciseSets":[{"setType":"ACTIVE","startTime":"2026-01-31T06:12:00.0",` +
		`"duration":45.0,"repetitionCount":12,"weight":20000.0,"messageIndex":0,` +
		`"exercises":[{"category":"BENCH_PRESS","name":"BARBELL_BENCH_PRESS","probability":95}]}]}`
)

// activityArray renders one synthetic activity per identifier. The coordinates are
// present on purpose: a tool must not pass them on.
func activityArray(ids ...int64) string {
	entries := make([]string, 0, len(ids))
	for i, id := range ids {
		entries = append(entries, `{"activityId":`+strconv.FormatInt(id, 10)+
			`,"activityName":"Synthetic run `+strconv.Itoa(i)+`"`+
			`,"startTimeLocal":"2026-01-31 06:12:00","startTimeGMT":"2026-01-31 05:12:00"`+
			`,"activityType":{"typeKey":"running"},"eventType":{"typeKey":"uncategorized"}`+
			`,"distance":10000.0,"duration":3000.0,"elapsedDuration":3060.0,"movingDuration":2980.0`+
			`,"calories":640,"averageHR":148,"maxHR":172`+
			`,"startLatitude":0.0,"startLongitude":0.0,"favorite":false}`)
	}
	return "[" + strings.Join(entries, ",") + "]"
}

// readScript scripts every endpoint the read-only tool surface reaches.
func readScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(testkit.JSON(http.StatusOK, profileBody), 8)...).
		With(client.PathUserSettings, repeat(testkit.JSON(http.StatusOK, settingsBody), 4)...).
		With(client.PathActivitySearch, testkit.JSON(http.StatusOK, activityArray(9001, 9002))).
		With(sleepPath(), testkit.JSON(http.StatusOK, sleepBody)).
		With(summaryPath(), testkit.JSON(http.StatusOK, summaryBody)).
		With(client.PathDevices, testkit.JSON(http.StatusOK, devicesBody)).
		With(activityDetailPath(client.SegmentTypedSplits),
			testkit.JSON(http.StatusOK, `{"lapDTOs":[`+splitEntry+`]}`)).
		With(activityDetailPath(client.SegmentExerciseSets),
			testkit.JSON(http.StatusOK, exerciseSetsBody))
}

// repeat returns n copies of behavior, because the fake serves one queued behavior
// per request and several tools read the profile more than once.
func repeat(behavior testkit.Behavior, n int) []testkit.Behavior {
	out := make([]testkit.Behavior, 0, n)
	for range n {
		out = append(out, behavior)
	}
	return out
}
