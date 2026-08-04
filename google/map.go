package google

import (
	"encoding/json"
	"time"

	"github.com/jamesjohnsdev/calgate"
)

type gCalendar struct {
	ID      string          `json:"id"`
	Summary string          `json:"summary"`
	Primary bool            `json:"primary"`
	raw     json.RawMessage // populated by custom UnmarshalJSON
}

func (c *gCalendar) UnmarshalJSON(b []byte) error {
	type alias gCalendar
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = gCalendar(a)
	c.raw = b
	return nil
}

func (c gCalendar) toCalendar() calgate.Calendar {
	return calgate.Calendar{
		ID:       c.ID,
		Name:     c.Summary,
		Primary:  c.Primary,
		Provider: calgate.ProviderGoogle,
		Raw:      c.raw,
	}
}

type gDateTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
}

// parse returns the time and whether it is an all-day (date-only) value.
// Returns zero time on parse failure; all-day is determined by which field is set.
func (dt gDateTime) parse() (time.Time, bool) {
	if dt.Date != "" {
		t, _ := time.ParseInLocation("2006-01-02", dt.Date, time.UTC)
		return t, true
	}
	if dt.DateTime != "" {
		t, _ := time.Parse(time.RFC3339, dt.DateTime)
		return t, false
	}
	return time.Time{}, false
}

type gOrganizer struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type gAttendee struct {
	DisplayName    string `json:"displayName"`
	Email          string `json:"email"`
	ResponseStatus string `json:"responseStatus"`
}

func (a gAttendee) toAttendee() calgate.Attendee {
	resp := a.ResponseStatus
	switch resp {
	case "accepted", "declined", "tentative":
	default:
		resp = "none"
	}
	return calgate.Attendee{
		Name:     a.DisplayName,
		Email:    a.Email,
		Response: resp,
	}
}

type gEvent struct {
	ID          string      `json:"id"`
	Summary     string      `json:"summary"`
	Description string      `json:"description"`
	Location    string      `json:"location"`
	Status      string      `json:"status"`
	Start       gDateTime   `json:"start"`
	End         gDateTime   `json:"end"`
	Organizer   gOrganizer  `json:"organizer"`
	Attendees   []gAttendee `json:"attendees"`
	Recurrence  []string    `json:"recurrence"`
	raw         json.RawMessage
}

func (e *gEvent) UnmarshalJSON(b []byte) error {
	type alias gEvent
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*e = gEvent(a)
	e.raw = b
	return nil
}

func (e gEvent) toEvent() calgate.Event {
	start, _ := e.Start.parse()
	end, endAllDay := e.End.parse()
	_, startAllDay := e.Start.parse()
	allDay := startAllDay || endAllDay

	organizer := e.Organizer.DisplayName
	if organizer == "" {
		organizer = e.Organizer.Email
	}

	var recurrence string
	if len(e.Recurrence) > 0 {
		recurrence = e.Recurrence[0]
	}

	attendees := make([]calgate.Attendee, len(e.Attendees))
	for i, a := range e.Attendees {
		attendees[i] = a.toAttendee()
	}

	return calgate.Event{
		ID:          e.ID,
		Title:       e.Summary,
		Description: e.Description,
		Location:    e.Location,
		Status:      e.Status,
		Start:       start,
		End:         end,
		AllDay:      allDay,
		Organizer:   organizer,
		Attendees:   attendees,
		Recurrence:  recurrence,
		Provider:    calgate.ProviderGoogle,
		Raw:         e.raw,
	}
}
