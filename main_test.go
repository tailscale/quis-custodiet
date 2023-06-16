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
		"id": "0000a0acd3057ef84015245f",
		"name": "Control oncall",
		"slug": "control-oncall",
		"organization_id": "00006b69a19765b2c39da2ee",
		"colour": "#0f61dd",
		"description": "",
		"owner": {
		  "id": "00006b69a19765b2c39da1f3",
		  "type": "team"
		},
		"access_control": [],
		"organization": {
		  "id": "00006b69a19765b2c39da1ee",
		  "name": "Tailscale, Inc",
		  "slug": "tailscale-inc"
		}
	  },
	  {
		"id": "00007ed13fab4f7fedc44d42",
		"name": "Rotation 2",
		"slug": "rotation-2",
		"organization_id": "00006b69a19765b2c39da1ee",
		"colour": "#0f61dd",
		"description": "",
		"owner": {
		  "id": "00006b69a19765b2c39da1f3",
		  "type": "team"
		},
		"access_control": [],
		"organization": {
		  "id": "00006b69a19765b2c39da1ee",
		  "name": "Tailscale, Inc",
		  "slug": "tailscale-inc"
		}
	  }
	]
  }`

const oncallGood = `{
	"data": [{
	  "oncall": [
		{
		  "id": "00005186f329137034b38419",
		  "first_name": "Awesome",
		  "last_name": "Possum",
		  "email": "possum@tailscale.com",
		  "contact": {
			"dial_code": "+44",
			"phone_number": "5551111111"
		  }
		}
	  ]
      }]
  }`

const oncallBad = `{
	"data": [{
	  "oncall": []
      }]
  }`

const eventsGood = `{
	"data": [
	  {
		"id": "00007f473fab4f7fedc44d46",
		"calendar_id": "000000000000000000000000",
		"start_time": "2023-02-02T00:00:00Z",
		"end_time": "2023-02-03T00:00:00Z",
		"name": "Possum Test Shift",
		"user_ids": [
		  "00005186f329137034b38419"
		],
		"series_id": "00007f473fab4f7fedc44d45",
		"squad_ids": [],
		"is_override": false,
		"schedule_id": "00007ed13fab4f7fedc44d42",
		"calendar": {
		  "id": "00007ed13fab4f7fedc44d42",
		  "name": "",
		  "slug": ""
		}
	  }
	]}`

const eventsBad = `{"data": [ ]}`

const graphqlGood = `{
  "data": {
    "schedules": [
      {
        "teamID": "0000a0acd3057ef84015245f",
        "name": "Schedule 1",
        "ID": 1
      },
      {
        "teamID": "0000a0acd3057ef84015245f",
        "name": "Schedule 2",
        "ID": 2
      }
    ]
  }
}`

func TestSquadcast(t *testing.T) {
	for _, tt := range []struct {
		name      string
		now       time.Time
		responses map[string]string // url -> json
		wantOK    bool
	}{
		{"all_responses_404", time.Time{}, map[string]string{}, false},
		{"no_schedules", time.Time{}, map[string]string{"/v3/schedules": `{"data": []}`}, false},
		{"nobody_oncall_now", time.Date(2023, 02, 01, 12, 00, 00, 0, time.UTC), map[string]string{
			"/v3/schedules":                                 schedules,
			"/v4/schedules/who-is-oncall":                   oncallBad,
			"/v3/schedules/0000a0acd3057ef84015245f/events": eventsGood,
		}, false},
		{"nobody_oncall_tomorrow", time.Date(2024, 12, 12, 12, 12, 12, 12, time.UTC), map[string]string{
			"/v3/schedules": schedules,
			"/v3/schedules/0000a0acd3057ef84015245f/events": eventsGood,
		}, false},
		{"no_oncall_shifts", time.Date(2024, 12, 12, 12, 12, 12, 12, time.UTC), map[string]string{
			"/v3/schedules": schedules,
			"/v3/schedules/0000a0acd3057ef84015245f/events": eventsBad,
		}, false},
		{"all_good", time.Date(2023, 02, 01, 12, 00, 00, 0, time.UTC), map[string]string{
			"/v3/schedules": schedules,
			"/v3/schedules/0000a0acd3057ef84015245f/events": eventsGood,
		}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tt.responses["/v3/graphql"] = graphqlGood
				// Always return "good" responses for the second rotation, to avoid repeating this in tests.
				tt.responses["/v3/schedules/00007ed13fab4f7fedc44d42/events"] = eventsGood
				if r, ok := tt.responses[r.URL.Path]; ok {
					w.Write([]byte(r))
					return
				}
				if r.URL.Path == "/v4/schedules/who-is-oncall" {
					q := r.URL.Query()
					if q.Get("teamID") == "00007ed13fab4f7fedc44d42" {
						// second rotation, return good
						w.Write([]byte(oncallGood))
						return
					}
					at := q.Get("at")
					timenow := tt.now
					var err error
					if at != "" {
						timenow, err = time.Parse(time.RFC3339, at)
						if err != nil {
							t.Errorf("time.Parse bad at= parameter: %q", err)
						}
					}
					if timenow.Year() > 1970 && timenow.Year() < 2024 {
						w.Write([]byte(oncallGood))
						return
					} else {
						w.Write([]byte(oncallBad))
						return
					}
				}
				w.WriteHeader(404)
			}))
			defer ts.Close()

			s := &squadcast{
				baseURL:      ts.URL,
				now:          func() time.Time { return tt.now },
				futureWindow: 24 * time.Hour,
			}
			if got := s.isOK("token", "0000a0acd3057ef84015245f"); got != tt.wantOK {
				t.Errorf("IsOk() = %v; want %v", got, tt.wantOK)
			}
		})
	}
}
