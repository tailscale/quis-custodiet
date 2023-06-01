// quis-custodiet checks the health of Tailscale's instance on squadcast.com,
// and whether anyone is oncall.
// Based substantially on https://github.com/SquadcastHub/who-s-oncall-slack
//
// To redeploy, run `./build-local.sh`.
package main

import (
	"encoding/json"
	"expvar"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"tailscale.com/tsweb"
	"tailscale.com/util/httpm"
	"tailscale.com/version"
)

var (
	apiRefreshToken = os.Getenv("SQUADCAST_REFRESH_TOKEN")
	squadcastOncall = expvar.NewInt("gauge_squadcast_oncall")
	squadcastPolls  = expvar.NewInt("squadcast_polls")
	squadcastActive = expvar.NewInt("squadcast_active")
)

type AccessTokenResponse struct {
	Data AccessTokenDetails `json:"data"`
}

type AccessTokenDetails struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int    `json:"expires_at"`
	IssuedAt    int    `json:"issued_at"`
}

type OnCallResponse struct {
	Data OnCallDetails `json:"data"`
}

type OnCallDetails struct {
	ShiftType string        `json:"shift_type"`
	Users     []UserDetails `json:"users"`
}

type UserDetails struct {
	ID        string `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type SchedulesResponse struct {
	Data []SchedulesDetails `json:"data"`
}

type SchedulesDetails struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type EventsResponse struct {
	Data []EventDetails `json:"data"`
}

type EventDetails struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	UserIDs   []string  `json:"user_ids"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

type squadcast struct {
	baseURL string
	now     func() time.Time

	// How often to check.
	interval time.Duration

	// How far in the future to look for verifying that someone is oncall.
	futureWindow time.Duration
}

func (s *squadcast) GetAccessToken() (token string, seconds int, err error) {
	url := s.baseURL + "/v3/oauth/access-token"
	req, err := http.NewRequest(httpm.GET, url, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("X-Refresh-Token", apiRefreshToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("%s responded %s", url, resp.Status)
	}

	var sq AccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&sq); err != nil {
		return "", 0, err
	}

	return sq.Data.AccessToken, (sq.Data.ExpiresAt - sq.Data.IssuedAt), nil
}

func (s *squadcast) get(path, token string, response any) error {
	url := s.baseURL + path
	req, err := http.NewRequest(httpm.GET, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s responded %s", url, resp.Status)
	}

	return json.NewDecoder(resp.Body).Decode(&response)
}
func (s *squadcast) isOK(token string) bool {
	if token == "" {
		log.Printf("Got empty token")
		return false
	}

	var sq SchedulesResponse
	if err := s.get("/v3/schedules", token, &sq); err != nil {
		log.Printf("ERROR: could not get schedules: %s", err)
		return false
	}

	if len(sq.Data) == 0 {
		log.Printf("Found no schedules: %+v", sq)
		return false
	}

	log.Printf("Fetched a list of schedules: %+v", sq.Data)

	for _, sch := range sq.Data {
		if !s.SomeoneOncallNow(token, sch.ID) {
			return false
		}
		if !s.someoneOncallAt(token, sch.ID, s.now().Add(s.futureWindow)) {
			return false
		}
	}

	return true
}

func (s *squadcast) poll() {
	ticker := time.NewTicker(s.interval)
	token, seconds, err := s.GetAccessToken()
	if err != nil {
		log.Fatalf("Could not get access token: %s", err)
	}
	refreshedAt := time.Now()

	for {
		squadcastPolls.Add(1)
		needsRefreshSeconds := time.Duration(seconds * 3 / 4)
		if time.Since(refreshedAt) > (needsRefreshSeconds * time.Second) {
			newToken, newSeconds, err := s.GetAccessToken()
			if err != nil {
				log.Printf("ERROR: could not refresh token: %s", err)
			} else {
				token = newToken
				seconds = newSeconds
				refreshedAt = time.Now()
			}
		}

		if s.isOK(token) {
			squadcastActive.Add(1)
			squadcastOncall.Set(1)
		} else {
			squadcastOncall.Set(0)
		}

		<-ticker.C
	}
}

func (s *squadcast) SomeoneOncallNow(token, scheduleID string) bool {
	var oc OnCallResponse
	if err := s.get(fmt.Sprintf("/v3/schedules/%s/on-call", scheduleID), token, &oc); err != nil {
		log.Printf("ERROR: could not get oncall for schedule %s: %s", scheduleID, err)
		return false

	}
	if len(oc.Data.Users) == 0 {
		log.Printf("Nobody is currently oncall for schedule %s", scheduleID)
		return false
	}
	log.Printf("Someone is currently oncall for schedule %s: %+v", scheduleID, oc.Data.Users)
	return true
}

func (s *squadcast) someoneOncallAt(token, scheduleID string, at time.Time) bool {
	var ev EventsResponse
	if err := s.get(fmt.Sprintf("/v3/schedules/%s/events", scheduleID), token, &ev); err != nil {
		log.Printf("ERROR: could not get events for schedule %s: %s", scheduleID, err)
		return false

	}
	for _, e := range ev.Data {
		if len(e.UserIDs) == 0 {
			continue
		}
		if e.StartTime.Before(at) && e.EndTime.After(at) {
			log.Printf("Someone is oncall for schedule %s at %s: %s", scheduleID, at, e.UserIDs)
			return true
		}
	}
	log.Printf("Nobody is oncall for schedule %s at %s", scheduleID, at)
	return false
}

func main() {
	portNum := flag.Int("port", 8080, "Port number for prometheus metrics")
	printVersion := flag.Bool("version", false, "print version and exit")
	interval := flag.Duration("interval", 15*time.Minute, "how often to check for oncalls")
	futureWindow := flag.Duration("future_window", 72*time.Hour, "how far in the future to check for existence of oncall")

	flag.Parse()

	if *printVersion {
		fmt.Println(version.Long())
		return
	}

	mux := http.NewServeMux()
	tsweb.Debugger(mux)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", *portNum),
		Handler:      mux,
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	s := &squadcast{
		baseURL:      "https://api.squadcast.com",
		now:          time.Now,
		interval:     *interval,
		futureWindow: *futureWindow,
	}
	go s.poll()

	err := srv.ListenAndServe()
	log.Fatal(err)
}
