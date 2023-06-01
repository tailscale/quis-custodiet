package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const schedules = `{
	"data": [
	  {
		"id": "000000000123456789abcdef",
		"name": "Control oncall",
		"slug": "control-oncall",
		"organization_id": "100000000123456789abcdef",
		"colour": "#0f61dd",
		"description": "",
		"owner": {
		  "id": "200000000123456789abcdef",
		  "type": "team"
		},
		"access_control": [],
		"organization": {
		  "id": "300000000123456789abcdef",
		  "name": "Example, Inc",
		  "slug": "example-inc"
		}
	  },
	  {
		"id": "400000000123456789abcdef",
		"name": "Rotation 2",
		"slug": "rotation-2",
		"organization_id": "300000000123456789abcdef",
		"colour": "#0f61dd",
		"description": "",
		"owner": {
		  "id": "200000000123456789abcdef",
		  "type": "team"
		},
		"access_control": [],
		"organization": {
		  "id": "300000000123456789abcdef",
		  "name": "Example, Inc",
		  "slug": "example-inc"
		}
	  }
	]
  }`

const oncallGood = `{
	"data": {
	  "shift_type": "Normal Shift",
	  "users": [
		{
		  "id": "500000000123456789abcdef",
		  "first_name": "Awesome",
		  "last_name": "Possum",
		  "email": "possum@example.com",
		  "contact": {
			"dial_code": "+44",
			"phone_number": "5551111111"
		  }
		}
	  ]
	}
  }`

const oncallBad = `{
	"data": {
	  "shift_type": "Normal Shift",
	  "users": []
	}
  }`

const eventsGood = `{
	"data": [
	  {
		"id": "600000000123456789abcdef",
		"calendar_id": "000000000000000000000000",
		"start_time": "2023-02-02T00:00:00Z",
		"end_time": "2023-02-03T00:00:00Z",
		"name": "Possum Test Shift",
		"user_ids": [
		  "500000000123456789abcdef"
		],
		"series_id": "700000000123456789abcdef",
		"squad_ids": [],
		"is_override": false,
		"schedule_id": "400000000123456789abcdef",
		"calendar": {
		  "id": "400000000123456789abcdef",
		  "name": "",
		  "slug": ""
		}
	  }
	]}`

const eventsBad = `{"data": [ ]}`

func TestSquadcast(t *testing.T) {
	for _, tt := range []struct {
		name      string
		now       time.Time
		responses map[string]string // url -> json
		wantOK    bool
	}{
		{"all responses 404", time.Time{}, map[string]string{}, false},
		{"no schedules", time.Time{}, map[string]string{"/v3/schedules": `{"data": []}`}, false},
		{"nobody oncall now", time.Date(2023, 02, 01, 12, 00, 00, 0, time.UTC), map[string]string{
			"/v3/schedules": schedules,
			"/v3/schedules/000000000123456789abcdef/on-call": oncallBad,
			"/v3/schedules/000000000123456789abcdef/events":  eventsGood,
		}, false},
		{"nobody oncall tomorrow", time.Date(2024, 12, 12, 12, 12, 12, 12, time.UTC), map[string]string{
			"/v3/schedules": schedules,
			"/v3/schedules/000000000123456789abcdef/on-call": oncallGood,
			"/v3/schedules/000000000123456789abcdef/events":  eventsGood,
		}, false},
		{"no oncall shifts", time.Date(2024, 12, 12, 12, 12, 12, 12, time.UTC), map[string]string{
			"/v3/schedules": schedules,
			"/v3/schedules/000000000123456789abcdef/on-call": oncallGood,
			"/v3/schedules/000000000123456789abcdef/events":  eventsBad,
		}, false},
		{"all good", time.Date(2023, 02, 01, 12, 00, 00, 0, time.UTC), map[string]string{
			"/v3/schedules": schedules,
			"/v3/schedules/000000000123456789abcdef/on-call": oncallGood,
			"/v3/schedules/000000000123456789abcdef/events":  eventsGood,
		}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Always return "good" responses for the second rotation, to avoid repeating this in tests.
				tt.responses["/v3/schedules/400000000123456789abcdef/on-call"] = oncallGood
				tt.responses["/v3/schedules/400000000123456789abcdef/events"] = eventsGood
				if r, ok := tt.responses[r.URL.Path]; ok {
					w.Write([]byte(r))
					return
				}
				w.WriteHeader(404)
			}))
			defer ts.Close()

			s := &squadcast{
				baseURL:      ts.URL,
				now:          func() time.Time { return tt.now },
				futureWindow: 24 * time.Hour,
			}
			if got := s.isOK("token"); got != tt.wantOK {
				t.Errorf("IsOk() = %v; want %v", got, tt.wantOK)
			}
		})
	}
}
