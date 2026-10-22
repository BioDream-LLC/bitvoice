package main

// The invented 911 calls the demo places. Nothing here is a real call or a real caller: the numbers are
// 555-01xx numbers, kept for fiction, and the places are ordinary streets of Roanoke, Virginia (where the
// bitdispatcher demo's map and units are), so a CAD's map has something to show.

type line struct {
	taker bool // said by the call-taker; otherwise by the caller
	text  string
}

type scenario struct {
	name     string
	caller   string // the callback number, E.164
	service  string // urn:service:sos...
	hno, rd  string // house number and street, without the suffix
	sts      string // street suffix
	pod      string // the street's quadrant, after it (Roanoke: NE, NW, SE, SW)
	city, st string
	lat, lon string
	lines    []line
}

var scenarios = []scenario{
	{name: "kitchen fire", caller: "+15405550142", service: "urn:service:sos.fire",
		hno: "1210", rd: "Williamson", sts: "Rd", pod: "NE", city: "Roanoke", st: "VA", lat: "37.2851", lon: "-79.9362",
		lines: []line{
			{true, "911, what is the address of your emergency?"},
			{false, "1210 Williamson Road Northeast. There's a fire in my kitchen, the pan caught and it's spreading up the cabinets."},
			{true, "Is everyone out of the house?"},
			{false, "Yes, we're all outside, me and my two kids. Nobody's hurt."},
			{true, "Okay. Do not go back inside. Is there smoke coming from the roof or windows?"},
			{false, "Smoke from the kitchen window, the back of the house. I can see flames now."},
			{true, "The fire department is on the way. Stay across the street and keep the kids with you."},
			{false, "Okay, we're across the street. Thank you."},
		}},
	{name: "two-car crash", caller: "+15405550117", service: "urn:service:sos",
		hno: "1500", rd: "Orange", sts: "Ave", pod: "NE", city: "Roanoke", st: "VA", lat: "37.2846", lon: "-79.9268",
		lines: []line{
			{true, "911, what is the address of your emergency?"},
			{false, "I'm on Orange Avenue at Williamson Road, there's been a crash, two cars."},
			{true, "Is anyone hurt?"},
			{false, "The other driver is bleeding from his head. He's awake, he's talking."},
			{true, "Are you hurt?"},
			{false, "No, I'm okay, my airbag went off. The cars are blocking the road."},
			{true, "Is there any fire or leaking fuel?"},
			{false, "No fire. There's some fluid on the road, I think it's coolant."},
			{true, "Help is on the way. Don't move him unless he's in danger."},
		}},
	{name: "neighbour fell", caller: "+15405550163", service: "urn:service:sos",
		hno: "2410", rd: "Melrose", sts: "Ave", pod: "NW", city: "Roanoke", st: "VA", lat: "37.2869", lon: "-79.9718",
		lines: []line{
			{true, "911, what is the address of your emergency?"},
			{false, "2410 Melrose Avenue. My neighbour fell down her stairs, she's eighty-two."},
			{true, "Is she awake and breathing?"},
			{false, "Yes, she's breathing. She says her hip hurts and she can't get up."},
			{true, "Did she hit her head?"},
			{false, "She says she didn't. No bleeding that I can see."},
			{true, "Keep her still and warm. An ambulance is on the way. Is the front door unlocked?"},
			{false, "I'll unlock it and turn the porch light on."},
		}},
	{name: "chest pain, address corrected", caller: "+15405550188", service: "urn:service:sos",
		hno: "42", rd: "Market", sts: "St", pod: "SE", city: "Roanoke", st: "VA", lat: "37.2707", lon: "-79.9393",
		lines: []line{
			{true, "911, what is the address of your emergency?"},
			{false, "24 Market Street. No, sorry, 42. 42 Market Street, the second floor."},
			{true, "42 Market Street, second floor. What's happening?"},
			{false, "My husband has chest pain. He's sweating and says his left arm feels heavy."},
			{true, "How old is he, and is he awake?"},
			{false, "He's sixty-one. He's awake, he's sitting on the couch."},
			{true, "Does he have any aspirin he's allowed to take?"},
			{false, "Yes, I'll get it. Please hurry."},
			{true, "The ambulance is on the way. I'll stay on the line with you."},
		}},
	{name: "gas smell", caller: "+15405550129", service: "urn:service:sos.fire",
		hno: "1802", rd: "Grandin", sts: "Rd", pod: "SW", city: "Roanoke", st: "VA", lat: "37.2603", lon: "-79.9752",
		lines: []line{
			{true, "911, what is the address of your emergency?"},
			{false, "1802 Grandin Road, the apartment building. It smells really strongly of gas in the hallway."},
			{true, "Are you inside the building now?"},
			{false, "I'm in the lobby. Other people are still in their apartments."},
			{true, "Please go outside now and don't switch any lights on or off."},
			{false, "Okay, I'm going out. I'll tell the people on the first floor."},
			{true, "The fire department and the gas company are on the way."},
		}},
}

// lineIndex numbers every line of every scenario, in order: the tone a line is played as.
func lineIndex(s, l int) int {
	n := 0
	for i := 0; i < s; i++ {
		n += len(scenarios[i].lines)
	}
	return n + l
}

// lineAt is the text of the line numbered k.
func lineAt(k int) (string, bool) {
	for _, sc := range scenarios {
		if k < len(sc.lines) {
			return sc.lines[k].text, true
		}
		k -= len(sc.lines)
	}
	return "", false
}

func totalLines() int { return lineIndex(len(scenarios), 0) }
