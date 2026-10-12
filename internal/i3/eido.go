package i3

import (
	"encoding/json"
	"strings"
	"time"
)

// Incident is what BitVoice knows about an emergency call, for the EIDO it sends to CAD.
type Incident struct {
	ID          string // BitVoice's call ID
	Agency      string // the PSAP's agency identifier (a domain, like psap.county.pa.us)
	Element     string // this element's identifier (BitVoice's host name)
	Queue       string // the queue or number the call came in on
	From        string // callback number
	CallerName  string
	Agent       string // the call-taker (user name)
	Position    string // their position (extension)
	Start       time.Time
	Answer      time.Time
	State       string // callAnswered, callTransferred, callTerminated...
	Location    *Location
	LocationRaw []byte
	Additional  Additional
	Comments    []string
}

func urnID(kind, id, element string) string {
	return "urn:emergency:uid:" + kind + ":" + id + ":" + element
}

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02T15:04:05.000Z07:00")
}

type ref struct {
	Ref string `json:"$ref"`
}

// EIDO builds the Emergency Incident Data Object (NENA-STA-024) for an incident: the call, its
// callback, the agency and agent, the location (by value) and the Additional Data.
func EIDO(in Incident, now time.Time) ([]byte, error) {
	agency := ref{in.Agency}
	agentID := in.Agent
	if agentID != "" && !strings.Contains(agentID, "@") {
		agentID += "@" + in.Agency
	}
	callID := urnID("callid", in.ID, in.Element)
	cbID := "callback-" + in.ID
	locID := "location-" + in.ID
	adrID := "additional-" + in.ID
	call := map[string]any{
		"$id": callID, "lastUpdateTimeStamp": ts(now), "updatedByAgencyReference": agency,
		"queueIdentifier": in.Queue, "standardPrimaryCallType": "emergency", "direction": "incoming",
		"callStartTimestamp": ts(in.Start), "callStateRegistryText": firstNonEmpty(in.State, "callAnswered"),
		"callBackReference": ref{cbID},
	}
	if !in.Answer.IsZero() {
		call["answerDate"] = ts(in.Answer)
	}
	if agentID != "" {
		call["agentReference"] = []ref{{agentID}}
		call["updatedByAgentReference"] = ref{agentID}
	}
	doc := map[string]any{
		"$id": urnID("incidentid", in.ID, in.Element), "lastUpdateTimeStamp": ts(now),
		"updatedByAgencyReference": agency, "eidoVersion": "1.0", "issuingElementIdentification": in.Element,
		"callComponent": []any{call},
		"callbackComponent": []any{map[string]any{"$id": cbID, "lastUpdateTimeStamp": ts(now), "updatedByAgencyReference": agency,
			"callBackInformationUri": []string{"tel:" + in.From}}},
		"agencyComponent": []any{map[string]any{"$id": in.Agency, "lastUpdateTimeStamp": ts(now), "updatedByAgencyReference": agency,
			"agencyRoleDescriptionRegistryText": []string{"psap"}}},
		"@context": map[string]string{"@vocab": "./JSON-LD_Contexts/EmergencyIncidentDataObjectType.jsonld"},
	}
	if agentID != "" {
		agent := map[string]any{"$id": agentID, "lastUpdateTimeStamp": ts(now), "updatedByAgencyReference": agency,
			"agencyReference": agency, "agentRoleRegistryText": []string{"callTaker"}}
		if in.Position != "" {
			agent["agentWorkstationPositionIdentification"] = in.Position
		}
		doc["agentComponent"] = []any{agent}
		doc["updatedByAgentReference"] = ref{agentID}
	}
	if len(in.LocationRaw) > 0 {
		call["locationReference"] = []ref{{locID}}
		doc["locationComponent"] = []any{map[string]any{"$id": locID, "lastUpdateTimeStamp": ts(now), "updatedByAgencyReference": agency,
			"locationTypeDescriptionRegistryText": "CallerLocation", "locationByValue": string(in.LocationRaw)}}
	}
	if !in.Additional.Empty() || len(in.Comments) > 0 {
		b, _ := json.Marshal(in.Additional)
		comp := map[string]any{"$id": adrID, "lastUpdateTimeStamp": ts(now), "updatedByAgencyReference": agency,
			"additionalDataByValue": string(b)}
		call["additionalDataReference"] = []ref{{adrID}}
		doc["additionalDataComponent"] = []any{comp}
	}
	if len(in.Comments) > 0 {
		var notes []any
		for i, c := range in.Comments {
			notes = append(notes, map[string]any{"$id": "note-" + in.ID + "-" + itoa(i+1), "lastUpdateTimeStamp": ts(now),
				"updatedByAgencyReference": agency, "noteText": c})
		}
		doc["notesComponent"] = notes
	}
	return json.MarshalIndent(doc, "", "  ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
