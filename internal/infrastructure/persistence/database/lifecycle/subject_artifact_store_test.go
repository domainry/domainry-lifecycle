package lifecycle

import "testing"

func TestSubjectExportBindingIDsFitSharedArtifactKey(t *testing.T) {
	artifactID := subjectExportArtifactID("workspace-a", "request-a")
	requestID := subjectExportBindingID(artifactID, "request")
	subjectID := subjectExportBindingID(artifactID, "subject")
	if len(artifactID) > 96 || len(requestID) > 96 || len(subjectID) > 96 {
		t.Fatalf("shared Artifact IDs exceed the 96-byte key contract: artifact=%d request=%d subject=%d", len(artifactID), len(requestID), len(subjectID))
	}
	if requestID == subjectID || requestID != subjectExportBindingID(artifactID, "request") {
		t.Fatalf("subject export binding IDs are not stable and relation-specific: request=%q subject=%q", requestID, subjectID)
	}
}
