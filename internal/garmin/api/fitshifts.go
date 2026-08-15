package api

import (
	"sort"
	"time"
)

// This file is the electronic-shifting analysis: the gear changes a Di2 or eTap
// groupset records, classified against the cadence and the gradient the record
// stream carries at that instant.

// Shift quality bands, in revolutions per minute. Source: the upstream tool
// description, which defines the four bands this server reproduces.
const (
	shiftProactiveFloor = 70.0
	shiftSpunOutCeiling = 100.0
	maxShiftEvents      = 500
	shiftMatchSeconds   = 10.0
)

// The shift quality labels, which are upstream's.
const (
	shiftCoasting  = "coasting"
	shiftReactive  = "reactive"
	shiftProactive = "proactive"
	shiftSpunOut   = "spun_out"
)

// A FITShiftEvent is one gear change with the context it happened in.
type FITShiftEvent struct {
	Time      time.Time
	Front     bool
	FrontGear FITNumber
	RearGear  FITNumber
	Cadence   FITNumber
	Grade     FITNumber
	Quality   string
}

// A FITShiftSummary counts the gear changes of one ride.
type FITShiftSummary struct {
	Total      int
	Front      int
	Rear       int
	Proactive  int
	Reactive   int
	Coasting   int
	SpunOut    int
	Classified int
	Events     []FITShiftEvent
}

// summarizeShifts classifies every gear change against the cadence and the grade the
// record stream carries at that instant.
func summarizeShifts(shifts []FITShift, records []FITRecord) FITShiftSummary {
	summary := FITShiftSummary{Events: make([]FITShiftEvent, 0, min(len(shifts), maxShiftEvents))}
	for _, shift := range shifts {
		event := shiftEvent(shift, records)
		summary.Total++
		if shift.Front {
			summary.Front++
		} else {
			summary.Rear++
		}
		summary.count(event.Quality)
		if len(summary.Events) < maxShiftEvents {
			summary.Events = append(summary.Events, event)
		}
	}
	return summary
}

// count tallies one classified shift.
func (s *FITShiftSummary) count(quality string) {
	switch quality {
	case shiftCoasting:
		s.Coasting++
	case shiftReactive:
		s.Reactive++
	case shiftProactive:
		s.Proactive++
	case shiftSpunOut:
		s.SpunOut++
	default:
		return
	}
	s.Classified++
}

// shiftEvent pairs one gear change with the sample it happened in.
func shiftEvent(shift FITShift, records []FITRecord) FITShiftEvent {
	event := FITShiftEvent{
		Time:      shift.Time,
		Front:     shift.Front,
		FrontGear: shift.FrontGear,
		RearGear:  shift.RearGear,
	}
	record, ok := recordAt(records, shift.Time)
	if !ok {
		return event
	}
	event.Cadence, event.Grade = record.Cadence, record.Grade
	event.Quality = shiftQuality(record.Cadence)
	return event
}

// shiftQuality classifies one shift by the cadence it happened at.
func shiftQuality(cadence FITNumber) string {
	switch {
	case !cadence.OK:
		return ""
	case cadence.Value == 0:
		return shiftCoasting
	case cadence.Value < shiftProactiveFloor:
		return shiftReactive
	case cadence.Value > shiftSpunOutCeiling:
		return shiftSpunOut
	default:
		return shiftProactive
	}
}

// recordAt returns the sample at or just before an instant, when one is close enough
// to describe it.
func recordAt(records []FITRecord, at time.Time) (FITRecord, bool) {
	if len(records) == 0 {
		return FITRecord{}, false
	}
	index := sort.Search(len(records), func(i int) bool {
		return records[i].Time.After(at)
	})
	if index == 0 {
		return FITRecord{}, false
	}

	record := records[index-1]
	if at.Sub(record.Time).Seconds() > shiftMatchSeconds {
		return FITRecord{}, false
	}
	return record, true
}
