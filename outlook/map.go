package outlook

import (
	"encoding/json"
	"time"

	"github.com/jamesjohnsdev/calgate"
)

// graphCalendar mirrors the Microsoft Graph calendar resource (subset).
type graphCalendar struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	IsDefaultCal bool   `json:"isDefaultCalendar"`
	rawFrom      json.RawMessage // captured via UnmarshalJSON
}

func (c *graphCalendar) UnmarshalJSON(b []byte) error {
	type alias graphCalendar
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = graphCalendar(a)
	c.rawFrom = append(json.RawMessage(nil), b...)
	return nil
}

func (c graphCalendar) toCalendar() calgate.Calendar {
	return calgate.Calendar{
		ID:       c.ID,
		Name:     c.Name,
		Primary:  c.IsDefaultCal,
		Provider: calgate.ProviderOutlook,
		Raw:      c.rawFrom,
	}
}

// graphEvent mirrors the Microsoft Graph event resource (subset).
type graphEvent struct {
	ID          string        `json:"id"`
	Subject     string        `json:"subject"`
	BodyPreview string        `json:"bodyPreview"`
	IsAllDay    bool          `json:"isAllDay"`
	IsCancelled bool          `json:"isCancelled"`
	ShowAs      string        `json:"showAs"`
	Start       graphDateTime `json:"start"`
	End         graphDateTime `json:"end"`
	Location    struct {
		DisplayName string `json:"displayName"`
	} `json:"location"`
	Organizer struct {
		EmailAddress graphEmail `json:"emailAddress"`
	} `json:"organizer"`
	Attendees []struct {
		Type   string     `json:"type"`
		Status struct{ Response string } `json:"status"`
		Email  graphEmail `json:"emailAddress"`
	} `json:"attendees"`
	Recurrence json.RawMessage `json:"recurrence"`

	rawFrom json.RawMessage
}

type graphEmail struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type graphDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// parse resolves a Graph dateTime+timeZone into a time.Time. Graph emits e.g.
// "2026-08-04T11:00:00.0000000" with a separate IANA/Windows zone name.
func (d graphDateTime) parse() time.Time {
	if d.DateTime == "" {
		return time.Time{}
	}
	loc := time.UTC
	if d.TimeZone != "" && d.TimeZone != "UTC" {
		if l, err := time.LoadLocation(d.TimeZone); err == nil {
			loc = l
		}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.0000000",
		"2006-01-02T15:04:05",
		time.RFC3339,
	} {
		if t, err := time.ParseInLocation(layout, d.DateTime, loc); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (e *graphEvent) UnmarshalJSON(b []byte) error {
	type alias graphEvent
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*e = graphEvent(a)
	e.rawFrom = append(json.RawMessage(nil), b...)
	return nil
}

func (e graphEvent) toEvent() calgate.Event {
	status := "confirmed"
	if e.IsCancelled {
		status = "cancelled"
	} else if e.ShowAs == "tentative" {
		status = "tentative"
	}

	var attendees []calgate.Attendee
	for _, a := range e.Attendees {
		resp := a.Status.Response
		if resp == "" || resp == "none" {
			resp = "none"
		}
		attendees = append(attendees, calgate.Attendee{
			Name:     a.Email.Name,
			Email:    a.Email.Address,
			Response: resp,
		})
	}

	var rec string
	if len(e.Recurrence) > 0 && string(e.Recurrence) != "null" {
		rec = string(e.Recurrence)
	}

	return calgate.Event{
		ID:          e.ID,
		Title:       e.Subject,
		Description: e.BodyPreview,
		Start:       e.Start.parse(),
		End:         e.End.parse(),
		AllDay:      e.IsAllDay,
		Location:    e.Location.DisplayName,
		Organizer:   e.Organizer.EmailAddress.Address,
		Attendees:   attendees,
		Recurrence:  rec,
		Status:      status,
		Provider:    calgate.ProviderOutlook,
		Raw:         e.rawFrom,
	}
}
