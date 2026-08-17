package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func deleteCourseRegistration() newToolsRegistration {
	return newToolsRegistration{name: ToolDeleteCourse, register: registerDeleteCourse}
}

// TestDeleteCourseRemovesTheCourse drives delete_course through the real
// server with a confirming Confirmer, proving the destructive tier's
// confirmation middleware lets a confirmed call through.
func TestDeleteCourseRemovesTheCourse(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript().
		With(client.PathCourseBase+"/9001", testkit.JSON(http.StatusOK, `{}`)))
	server := newToolsServer(t, svc, newToolsServerConfig{
		destructive: []newToolsRegistration{deleteCourseRegistration()},
	})
	session := newToolsSession(t, server)

	out := newToolsCall(t, session, ToolDeleteCourse, map[string]any{argCourseID: 9001})
	if out[argCourseID] != float64(9001) {
		t.Errorf("course_id = %v, want 9001", out[argCourseID])
	}
	if out["message"] == "" {
		t.Error("message is empty")
	}

	requests := fake.Requests()
	if len(requests) != 1 || requests[0].Method != http.MethodDelete {
		t.Errorf("requests = %+v, want one DELETE", requests)
	}
}

func TestDeleteCourseRefusesAZeroIdentifier(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript())
	server := newToolsServer(t, svc, newToolsServerConfig{
		destructive: []newToolsRegistration{deleteCourseRegistration()},
	})
	session := newToolsSession(t, server)

	message := newToolsCallError(t, session, ToolDeleteCourse, map[string]any{argCourseID: 0})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestDeleteCourseDeclaresTheDestructiveTier(t *testing.T) {
	t.Parallel()

	contract := deleteCourseContract()
	if contract.Spec.Tier.String() != "destructive" {
		t.Errorf("tier = %v, want destructive", contract.Spec.Tier)
	}
	if !contract.Spec.Annotations.Destructive {
		t.Error("annotations.Destructive = false, want true")
	}
}
