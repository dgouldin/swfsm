package fsm

import (
	"fmt"
	"strconv"

	"github.com/awslabs/aws-sdk-go/aws"
	"github.com/awslabs/aws-sdk-go/gen/swf"
)

// EventCorrelator is a serialization-friendly struct that is automatically managed by the FSM machinery
// It tracks signal and activity correlation info, so you know how to react when an event that signals the
// end of an activity or signal  hits your Decider.  This is missing from the SWF api.
// Activities and Signals are string instead of int64 beacuse json.
type EventCorrelator struct {
	Activities       map[string]*ActivityInfo //schedueledEventId -> info
	ActivityAttempts map[string]int           //activityID -> attempts
	Signals          map[string]*SignalInfo   //schedueledEventId -> info
	SignalAttempts   map[string]int           //? workflowID + signalName -> attempts
}

// ActivityInfo holds the ActivityID and ActivityType for an activity
type ActivityInfo struct {
	ActivityID string
	*swf.ActivityType
}

// SignalInfo holds the SignalName and Input for an activity
type SignalInfo struct {
	SignalName string
	WorkflowID string
}

// Track will add or remove entries based on the EventType.
// A new entry is added when there is a new ActivityTask, or an entry is removed when the ActivityTask is terminating.
func (a *EventCorrelator) Track(h swf.HistoryEvent) {
	a.RemoveCorrelation(h)
	a.Correlate(h)
}

// Correlate establishes a mapping of eventId to ActivityType. The HistoryEvent is expected to be of type EventTypeActivityTaskScheduled.
func (a *EventCorrelator) Correlate(h swf.HistoryEvent) {
	a.checkInit()

	if *h.EventType == swf.EventTypeActivityTaskScheduled {
		a.Activities[a.key(h.EventID)] = &ActivityInfo{
			ActivityID:   *h.ActivityTaskScheduledEventAttributes.ActivityID,
			ActivityType: h.ActivityTaskScheduledEventAttributes.ActivityType,
		}
	}

	if *h.EventType == swf.EventTypeSignalExternalWorkflowExecutionInitiated {
		a.Signals[a.key(h.EventID)] = &SignalInfo{
			SignalName: *h.SignalExternalWorkflowExecutionInitiatedEventAttributes.SignalName,
			WorkflowID: *h.SignalExternalWorkflowExecutionInitiatedEventAttributes.WorkflowID,
		}
		fmt.Printf("added signal @ %s\n %+v\n", a.key(h.EventID), a.Signals)
	}
}

// RemoveCorrelation gcs a mapping of eventId to ActivityType. The HistoryEvent is expected to be of type EventTypeActivityTaskCompleted,EventTypeActivityTaskFailed,EventTypeActivityTaskTimedOut.
func (a *EventCorrelator) RemoveCorrelation(h swf.HistoryEvent) {
	a.checkInit()

	switch *h.EventType {
	case swf.EventTypeActivityTaskCompleted:
		delete(a.ActivityAttempts, a.safeActivityID(h))
		delete(a.Activities, a.key(h.ActivityTaskCompletedEventAttributes.ScheduledEventID))
	case swf.EventTypeActivityTaskFailed:
		a.incrementActivityAttempts(h)
		delete(a.Activities, a.key(h.ActivityTaskFailedEventAttributes.ScheduledEventID))
	case swf.EventTypeActivityTaskTimedOut:
		a.incrementActivityAttempts(h)
		delete(a.Activities, a.key(h.ActivityTaskTimedOutEventAttributes.ScheduledEventID))
	case swf.EventTypeActivityTaskCanceled:
		delete(a.ActivityAttempts, a.safeActivityID(h))
		delete(a.Activities, a.key(h.ActivityTaskCanceledEventAttributes.ScheduledEventID))
	case swf.EventTypeExternalWorkflowExecutionSignaled:
		info := a.Signals[a.key(h.ExternalWorkflowExecutionSignaledEventAttributes.InitiatedEventID)]
		delete(a.SignalAttempts, a.signalIDFromInfo(info))
		delete(a.Signals, a.key(h.ExternalWorkflowExecutionSignaledEventAttributes.InitiatedEventID))
	case swf.EventTypeSignalExternalWorkflowExecutionFailed:
		a.incrementSignalAttempts(h)
		delete(a.Signals, a.key(h.SignalExternalWorkflowExecutionFailedEventAttributes.InitiatedEventID))
	}
}

// ActivityInfo returns the ActivityInfo that is correlates with a given event. The HistoryEvent is expected to be of type EventTypeActivityTaskCompleted,EventTypeActivityTaskFailed,EventTypeActivityTaskTimedOut.
func (a *EventCorrelator) ActivityInfo(h swf.HistoryEvent) *ActivityInfo {
	a.checkInit()
	return a.Activities[a.getID(h)]
}

// SignalInfo returns the SignalInfo that is correlates with a given event. The HistoryEvent is expected to be of type EventTypeSignalExternalWorkflowExecutionFailed,EventTypeExternalWorkflowExecutionSignaled.
func (a *EventCorrelator) SignalInfo(h swf.HistoryEvent) *SignalInfo {
	a.checkInit()
	return a.Signals[a.getID(h)]
}

//AttemptsForActivity returns the number of times a given activity has been attempted.
//It will return 0 if the activity has never failed, has been canceled, or has been completed successfully
func (a *EventCorrelator) AttemptsForActivity(info *ActivityInfo) int {
	a.checkInit()
	return a.ActivityAttempts[info.ActivityID]
}

//AttemptsForSignal returns the number of times a given signal has been attempted.
//It will return 0 if the signal has never failed, or has been completed successfully
func (a *EventCorrelator) AttemptsForSignal(signalInfo *SignalInfo) int {
	a.checkInit()
	return a.SignalAttempts[a.signalIDFromInfo(signalInfo)]
}

func (a *EventCorrelator) checkInit() {
	if a.Activities == nil {
		a.Activities = make(map[string]*ActivityInfo)
	}
	if a.ActivityAttempts == nil {
		a.ActivityAttempts = make(map[string]int)
	}
	if a.Signals == nil {
		a.Signals = make(map[string]*SignalInfo)
	}
	if a.SignalAttempts == nil {
		a.SignalAttempts = make(map[string]int)
	}
}

func (a *EventCorrelator) getID(h swf.HistoryEvent) (id string) {
	switch *h.EventType {
	case swf.EventTypeActivityTaskCompleted:
		id = a.key(h.ActivityTaskCompletedEventAttributes.ScheduledEventID)
	case swf.EventTypeActivityTaskFailed:
		id = a.key(h.ActivityTaskFailedEventAttributes.ScheduledEventID)
	case swf.EventTypeActivityTaskTimedOut:
		id = a.key(h.ActivityTaskTimedOutEventAttributes.ScheduledEventID)
	case swf.EventTypeActivityTaskCanceled:
		id = a.key(h.ActivityTaskCanceledEventAttributes.ScheduledEventID)
	case swf.EventTypeExternalWorkflowExecutionSignaled:
		id = a.key(h.ExternalWorkflowExecutionSignaledEventAttributes.InitiatedEventID)
	case swf.EventTypeSignalExternalWorkflowExecutionFailed:
		id = a.key(h.SignalExternalWorkflowExecutionFailedEventAttributes.InitiatedEventID)
	}
	return
}

func (a *EventCorrelator) safeActivityID(h swf.HistoryEvent) string {
	info := a.Activities[a.getID(h)]
	if info != nil {
		return info.ActivityID
	}
	return ""
}

func (a *EventCorrelator) safeSignalID(h swf.HistoryEvent) string {
	info := a.Signals[a.getID(h)]
	if info != nil {
		return a.signalIDFromInfo(info)
	}
	return ""
}

func (a *EventCorrelator) signalIDFromInfo(info *SignalInfo) string {
	return fmt.Sprintf("%s->%s", info.SignalName, info.WorkflowID)
}

func (a *EventCorrelator) incrementActivityAttempts(h swf.HistoryEvent) {
	id := a.safeActivityID(h)
	if id != "" {
		a.ActivityAttempts[id]++
	}
}

func (a *EventCorrelator) incrementSignalAttempts(h swf.HistoryEvent) {
	id := a.safeSignalID(h)
	if id != "" {
		a.SignalAttempts[id]++
	}
}

func (a *EventCorrelator) key(eventID aws.LongValue) string {
	return strconv.FormatInt(*eventID, 10)
}
