package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivityWeather is the upstream compatibility name of the weather read.
const ToolGetActivityWeather = "get_activity_weather"

// Temperature unit labels and the conversion Garmin's own answer needs.
const (
	unitFahrenheit  = "F"
	unitCelsius     = "C"
	metricUnitKey   = "metric"
	fahrenheitBase  = 32.0
	fahrenheitScale = 5.0 / 9.0
)

// ActivityWeather is the weather Garmin recorded for one activity.
//
// It deliberately carries no coordinate. Garmin answers this endpoint with the
// weather station's latitude and longitude, which place a person at the start of a
// real outing, and nothing in this result needs them.
type ActivityWeather struct {
	ActivityID       int64    `json:"activity_id" jsonschema:"the activity this weather belongs to"`
	Temperature      *float64 `json:"temperature,omitempty" jsonschema:"the recorded temperature"`
	ApparentTemp     *float64 `json:"apparent_temperature,omitempty" jsonschema:"the apparent temperature"`
	DewPoint         *float64 `json:"dew_point,omitempty" jsonschema:"the dew point"`
	TemperatureUnit  string   `json:"temperature_unit" jsonschema:"the unit the temperatures are in, C or F"`
	RelativeHumidity *float64 `json:"relative_humidity,omitempty" jsonschema:"the relative humidity in percent"`
	WindSpeed        *float64 `json:"wind_speed,omitempty" jsonschema:"the wind speed in the account's own unit"`
	WindDirection    *float64 `json:"wind_direction,omitempty" jsonschema:"the wind direction in degrees"`
	IssueDate        *string  `json:"issue_date,omitempty" jsonschema:"when the observation was issued"`
}

// LogValue reports that weather was read, never the observation.
func (w ActivityWeather) LogValue() slog.Value {
	return shape("activityWeather", slog.String("unit", w.TemperatureUnit))
}

func getActivityWeatherContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivityWeather,
			Title: "Get activity weather",
			Description: "read the weather Garmin recorded for one activity. Garmin serves " +
				"the temperature in Fahrenheit whatever the account prefers, so a metric " +
				"account gets Celsius here and the temperature_unit field states which unit " +
				"was returned. Wind speed already arrives in the account's own unit",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

func registerGetActivityWeather(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, ActivityWeather, error,
	) {
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityWeather{}, err
		}

		weather, err := svc.details.Weather(ctx, session, id)
		if err != nil {
			return nil, ActivityWeather{}, fail(err)
		}
		unitSystem, err := svc.profile.UnitSystem(ctx, session)
		if err != nil {
			return nil, ActivityWeather{}, fail(err)
		}
		return nil, newActivityWeather(id, weather, unitSystem), nil
	}
	return mcpserver.AddTool(registry, getActivityWeatherContract().Registration(), handler)
}

// newActivityWeather converts the temperatures into the account's display unit and
// drops the coordinates Garmin sends.
func newActivityWeather(id client.ID, weather api.Weather, unitSystem string) ActivityWeather {
	unit := unitFahrenheit
	convert := func(value *float64) *float64 { return value }
	if unitSystem == metricUnitKey {
		unit = unitCelsius
		convert = toCelsius
	}

	return ActivityWeather{
		ActivityID:       id.Int64(),
		Temperature:      convert(optionalFloat(weather.Temp)),
		ApparentTemp:     convert(optionalFloat(weather.ApparentTemp)),
		DewPoint:         convert(optionalFloat(weather.DewPoint)),
		TemperatureUnit:  unit,
		RelativeHumidity: optionalFloat(weather.RelativeHum),
		WindSpeed:        optionalFloat(weather.WindSpeed),
		WindDirection:    optionalFloat(weather.WindDirection),
		IssueDate:        weather.IssueDate,
	}
}

// toCelsius converts one Fahrenheit reading, returning a fresh value.
func toCelsius(value *float64) *float64 {
	if value == nil {
		return nil
	}
	converted := (*value - fahrenheitBase) * fahrenheitScale
	return &converted
}
