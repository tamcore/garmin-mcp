package client_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func TestCourseConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	paths := []struct {
		got  string
		want string
	}{
		{client.PathCourseBase, "/course-service/course"},
		{client.PathCourseImport, "/course-service/course/import"},
	}
	for _, tc := range paths {
		if tc.got != tc.want {
			t.Errorf("path = %q, want %q", tc.got, tc.want)
		}
	}

	labels := []struct {
		got  client.Endpoint
		want string
	}{
		{client.EndpointCourseList, "connectapi.course.list"},
		{client.EndpointCourseImport, "connectapi.course.import"},
		{client.EndpointCourseCreate, "connectapi.course.create"},
		{client.EndpointCourseDelete, "connectapi.course.delete"},
	}
	for _, tc := range labels {
		if string(tc.got) != tc.want {
			t.Errorf("endpoint label = %q, want %q", tc.got, tc.want)
		}
	}

	operations := []struct {
		got  client.Op
		want string
	}{
		{client.OpGetCourses, "get_courses"},
		{client.OpUploadCourse, "upload_course"},
		{client.OpDeleteCourse, "delete_course"},
	}
	for _, tc := range operations {
		if string(tc.got) != tc.want {
			t.Errorf("op = %q, want %q", tc.got, tc.want)
		}
	}
}
