package util

import (
	"fmt"
	"reflect"
	"testing"
)

func Test_nsxErrorImpl_setDetail(t *testing.T) {
	type fields struct {
		ErrorDetail ErrorDetail
		msg         string
	}
	type args struct {
		detail *ErrorDetail
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{ErrorDetail: ErrorDetail{}, msg: "msg"}, args{&ErrorDetail{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impl := nsxErrorImpl{
				ErrorDetail: tt.fields.ErrorDetail,
				msg:         tt.fields.msg,
			}
			impl.setDetail(tt.args.detail)
		})
	}
}

func Test_nsxErrorImpl_Error(t *testing.T) {
	type fields struct {
		ErrorDetail ErrorDetail
		msg         string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{"1", fields{ErrorDetail: ErrorDetail{}, msg: "msg"}, "msg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impl := nsxErrorImpl{
				ErrorDetail: tt.fields.ErrorDetail,
				msg:         tt.fields.msg,
			}
			if got := impl.Error(); got != tt.want {
				t.Errorf("nsxErrorImpl.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_createNsxLibException(t *testing.T) {
	tests := []struct {
		name string
		want *nsxErrorImpl
	}{
		{"1", &nsxErrorImpl{msg: "An unknown exception occurred."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := createNsxLibException(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("createNsxLibException() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateObjectAlreadyExists(t *testing.T) {
	type args struct {
		objectType string
	}
	tests := []struct {
		name string
		args args
		want *ObjectAlreadyExists
	}{
		{"1", args{"obj"}, &ObjectAlreadyExists{nsxErrorImpl{msg: fmt.Sprintf("%s already exists", "obj")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateObjectAlreadyExists(tt.args.objectType); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateObjectAlreadyExists() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNotImplemented(t *testing.T) {
	type args struct {
		operation string
	}
	tests := []struct {
		name string
		args args
		want *NotImplemented
	}{
		{"1", args{"obj"}, &NotImplemented{nsxErrorImpl{msg: fmt.Sprintf("%s is not supported", "obj")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNotImplemented(tt.args.operation); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNotImplemented() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateObjectNotGenerated(t *testing.T) {
	type args struct {
		objectType string
	}
	tests := []struct {
		name string
		args args
		want *ObjectNotGenerated
	}{
		{"1", args{"obj"}, &ObjectNotGenerated{nsxErrorImpl{msg: fmt.Sprintf("%s was not generated", "obj")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateObjectNotGenerated(tt.args.objectType); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateObjectNotGenerated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateCertificateError(t *testing.T) {
	type args struct {
		msg string
	}
	tests := []struct {
		name string
		args args
		want *CertificateError
	}{
		{"1", args{"obj"}, &CertificateError{nsxErrorImpl{msg: fmt.Sprintf("Certificate error: %s", "obj")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateCertificateError(tt.args.msg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateCertificateError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNsxLibInvalidInputImpl_nsxLibInvalidInput(t *testing.T) {
	type fields struct {
		nsxErrorImpl nsxErrorImpl
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{"1", fields{nsxErrorImpl{msg: fmt.Sprintf("%s is not supported", "obj")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NsxLibInvalidInputImpl{
				nsxErrorImpl: tt.fields.nsxErrorImpl,
			}
			n.nsxLibInvalidInput()
		})
	}
}

func TestCreateNsxLibInvalidInput(t *testing.T) {
	type args struct {
		errorMessage string
	}
	tests := []struct {
		name string
		args args
		want *GeneralNsxLibInvalidInput
	}{
		{"1", args{"obj"}, &GeneralNsxLibInvalidInput{NsxLibInvalidInputImpl{nsxErrorImpl{msg: fmt.Sprintf("Invalid input for operation: %s.", "obj")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxLibInvalidInput(tt.args.errorMessage); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxLibInvalidInput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_managerErrorImpl_managerError(t *testing.T) {
	type fields struct {
		nsxErrorImpl nsxErrorImpl
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{"1", fields{nsxErrorImpl{msg: fmt.Sprintf("%s is not supported", "obj")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impl := managerErrorImpl{
				nsxErrorImpl: tt.fields.nsxErrorImpl,
			}
			impl.managerError()
		})
	}
}

func TestCreateGeneralManagerError(t *testing.T) {
	type args struct {
		manager   string
		operation string
		details   string
	}
	tests := []struct {
		name string
		args args
		want *GeneralManagerError
	}{
		{"1", args{"manager", "operation", "details"}, &GeneralManagerError{managerErrorImpl{nsxErrorImpl{msg: fmt.Sprintf("Unexpected error from backend manager (%s) for %s%s", "manager", "operation", "details")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateGeneralManagerError(tt.args.manager, tt.args.operation, tt.args.details); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateGeneralManagerError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateResourceNotFound(t *testing.T) {
	type args struct {
		manager   string
		operation string
	}
	tests := []struct {
		name string
		args args
		want *ResourceNotFound
	}{
		{"1", args{"manager", "operation"}, &ResourceNotFound{managerErrorImpl{nsxErrorImpl{msg: fmt.Sprintf("Resource could not be found on backend (%s) for %s", "manager", "operation")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateResourceNotFound(tt.args.manager, tt.args.operation); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateResourceNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateMultipleResourcesFound(t *testing.T) {
	type args struct {
		manager   string
		operation string
	}
	tests := []struct {
		name string
		args args
		want *MultipleResourcesFound
	}{
		{"1", args{"manager", "operation"}, &MultipleResourcesFound{managerErrorImpl{nsxErrorImpl{msg: fmt.Sprintf("Multiple resources are found on backend (%s) for %s, where only one is expected", "manager", "operation")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateMultipleResourcesFound(tt.args.manager, tt.args.operation); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateMultipleResourcesFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateBackendResourceNotFound(t *testing.T) {
	type args struct {
		details   string
		manager   string
		operation string
	}
	tests := []struct {
		name string
		args args
		want BackendResourceNotFound
	}{
		{"1", args{"details", "manager", "operation"}, BackendResourceNotFound{managerErrorImpl{nsxErrorImpl{msg: fmt.Sprintf("%s On backend (%s) with Operation: %s", "details", "manager", "operation")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateBackendResourceNotFound(tt.args.details, tt.args.manager, tt.args.operation); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateBackendResourceNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateInvalidInput(t *testing.T) {
	type args struct {
		operation string
		argVal    string
		argName   string
	}
	tests := []struct {
		name string
		args args
		want *InvalidInput
	}{
		{"1", args{"operation", "argVal", "argName"}, &InvalidInput{managerErrorImpl{nsxErrorImpl{msg: fmt.Sprintf("%s failed: Invalid input %s for %s", "operation", "argVal", "argName")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateInvalidInput(tt.args.operation, tt.args.argVal, tt.args.argName); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateInvalidInput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateRealizationError(t *testing.T) {
	type args struct {
		operation string
		argVal    string
		argName   string
	}
	tests := []struct {
		name string
		args args
		want *RealizationError
	}{
		{"1", args{"operation", "argVal", "argName"}, &RealizationError{managerErrorImpl{nsxErrorImpl{msg: fmt.Sprintf("%s failed: Invalid input %s for %s", "operation", "argVal", "argName")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateRealizationError(tt.args.operation, tt.args.argVal, tt.args.argName); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateRealizationError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateRealizationErrorStateError(t *testing.T) {
	type args struct {
		resourceType string
		resourceID   string
		error        string
	}
	tests := []struct {
		name string
		args args
		want *RealizationErrorStateError
	}{
		{"1", args{"resourceType", "resourceID", "error"}, &RealizationErrorStateError{msg: fmt.Sprintf("%s ID %s is in ERROR state: %s", "resourceType", "resourceID", "error")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateRealizationErrorStateError(tt.args.resourceType, tt.args.resourceID, tt.args.error); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateRealizationErrorStateError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRealizationErrorStateError_Error(t *testing.T) {
	type fields struct {
		msg    string
		detail *ErrorDetail
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{"1", fields{"msg", &ErrorDetail{ErrorCode: 1}}, "msg" + fmt.Sprintf("StatusCode is %d,", 0) + fmt.Sprintf("ErrorCode is %d,", 1)},
		{"2", fields{"msg", &ErrorDetail{RelatedErrorCodes: []int{1, 2}}}, "msg" + fmt.Sprintf("StatusCode is %d,", 0) + fmt.Sprintf("RelatedErrorCodes is %v,", []int{1, 2})},
		{"3", fields{"msg", &ErrorDetail{RelatedStatusCodes: []string{"1", "2"}}}, "msg" + fmt.Sprintf("StatusCode is %d,", 0) + fmt.Sprintf("RelatedStatusCodes is %v,", []string{"1", "2"})},
		{"4", fields{"msg", &ErrorDetail{Details: "1"}}, "msg" + fmt.Sprintf("StatusCode is %d,", 0) + fmt.Sprintf("Detail is %s", "1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &RealizationErrorStateError{
				msg:    tt.fields.msg,
				detail: tt.fields.detail,
			}
			if got := e.Error(); got != tt.want {
				t.Errorf("RealizationErrorStateError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRealizationErrorStateError_setDetail(t *testing.T) {
	type fields struct {
		msg    string
		detail *ErrorDetail
	}
	type args struct {
		detail *ErrorDetail
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{"msg", &ErrorDetail{ErrorCode: 1}}, args{&ErrorDetail{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &RealizationErrorStateError{
				msg:    tt.fields.msg,
				detail: tt.fields.detail,
			}
			e.setDetail(tt.args.detail)
		})
	}
}

func TestCreateRealizationTimeoutError(t *testing.T) {
	type args struct {
		resourceType string
		resourceID   string
		attempts     string
		sleep        string
	}
	tests := []struct {
		name string
		args args
		want *RealizationTimeoutError
	}{
		{"1", args{"resourceType", "resourceID", "attempts", "sleep"}, &RealizationTimeoutError{msg: fmt.Sprintf("%s ID %s was not realized after %s attempts with %s seconds sleep", "resourceType", "resourceID", "attempts", "sleep")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateRealizationTimeoutError(tt.args.resourceType, tt.args.resourceID, tt.args.attempts, tt.args.sleep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateRealizationTimeoutError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRealizationTimeoutError_Error(t *testing.T) {
	type fields struct {
		msg    string
		detail *ErrorDetail
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{"1", fields{"msg", &ErrorDetail{Details: "msg"}}, "msg" + fmt.Sprintf("StatusCode is %d,", 0) + fmt.Sprintf("Detail is %s", "msg")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &RealizationTimeoutError{
				msg:    tt.fields.msg,
				detail: tt.fields.detail,
			}
			if got := e.Error(); got != tt.want {
				t.Errorf("RealizationTimeoutError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRealizationTimeoutError_setDetail(t *testing.T) {
	type fields struct {
		msg    string
		detail *ErrorDetail
	}
	type args struct {
		detail *ErrorDetail
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{"msg", &ErrorDetail{ErrorCode: 1}}, args{&ErrorDetail{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &RealizationTimeoutError{
				msg:    tt.fields.msg,
				detail: tt.fields.detail,
			}
			e.setDetail(tt.args.detail)
		})
	}
}

func TestCreateDetailedRealizationTimeoutError(t *testing.T) {
	type args struct {
		resourceType string
		resourceID   string
		realizedType string
		relatedType  string
		relatedID    string
		attempts     string
		sleep        string
	}
	tests := []struct {
		name string
		args args
		want *DetailedRealizationTimeoutError
	}{
		{"1", args{"resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep"}, &DetailedRealizationTimeoutError{msg: fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", "resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateDetailedRealizationTimeoutError(tt.args.resourceType, tt.args.resourceID, tt.args.realizedType, tt.args.relatedType, tt.args.relatedID, tt.args.attempts, tt.args.sleep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateDetailedRealizationTimeoutError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetailedRealizationTimeoutError_Error(t *testing.T) {
	type fields struct {
		msg    string
		detail *ErrorDetail
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{"1", fields{"msg", &ErrorDetail{Details: "msg"}}, "msg" + fmt.Sprintf("StatusCode is %d,", 0) + fmt.Sprintf("Detail is %s", "msg")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &DetailedRealizationTimeoutError{
				msg:    tt.fields.msg,
				detail: tt.fields.detail,
			}
			if got := e.Error(); got != tt.want {
				t.Errorf("DetailedRealizationTimeoutError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetailedRealizationTimeoutError_setDetail(t *testing.T) {
	type fields struct {
		msg    string
		detail *ErrorDetail
	}
	type args struct {
		detail *ErrorDetail
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{"msg", &ErrorDetail{ErrorCode: 1}}, args{&ErrorDetail{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &DetailedRealizationTimeoutError{
				msg:    tt.fields.msg,
				detail: tt.fields.detail,
			}
			e.setDetail(tt.args.detail)
		})
	}
}

func TestCreateStaleRevision(t *testing.T) {
	type args struct {
		resourceType string
		resourceID   string
		realizedType string
		relatedType  string
		relatedID    string
		attempts     string
		sleep        string
	}
	tests := []struct {
		name string
		args args
		want *StaleRevision
	}{
		{"1", args{"resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep"}, &StaleRevision{managerErrorImpl{nsxErrorImpl{msg: fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", "resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateStaleRevision(tt.args.resourceType, tt.args.resourceID, tt.args.realizedType, tt.args.relatedType, tt.args.relatedID, tt.args.attempts, tt.args.sleep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateStaleRevision() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServerBusyImpl_serverBusy(t *testing.T) {
	type fields struct {
		managerErrorImpl managerErrorImpl
		msg              string
		detail           *ErrorDetail
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{"1", fields{managerErrorImpl: managerErrorImpl{}, msg: "msg", detail: &ErrorDetail{ErrorCode: 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ServerBusyImpl{
				managerErrorImpl: tt.fields.managerErrorImpl,
				msg:              tt.fields.msg,
				detail:           tt.fields.detail,
			}
			s.serverBusy()
		})
	}
}

func TestCreateGeneralServerBusy(t *testing.T) {
	type args struct {
		resourceType string
		resourceID   string
		realizedType string
		relatedType  string
		relatedID    string
		attempts     string
		sleep        string
	}
	tests := []struct {
		name string
		args args
		want *GeneralServerBusy
	}{
		{"1", args{"resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep"}, &GeneralServerBusy{ServerBusyImpl{msg: fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", "resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateGeneralServerBusy(tt.args.resourceType, tt.args.resourceID, tt.args.realizedType, tt.args.relatedType, tt.args.relatedID, tt.args.attempts, tt.args.sleep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateGeneralServerBusy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateTooManyRequests(t *testing.T) {
	type args struct {
		resourceType string
		resourceID   string
		realizedType string
		relatedType  string
		relatedID    string
		attempts     string
		sleep        string
	}
	tests := []struct {
		name string
		args args
		want *TooManyRequests
	}{
		{"1", args{"resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep"}, &TooManyRequests{ServerBusyImpl{msg: fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", "resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateTooManyRequests(tt.args.resourceType, tt.args.resourceID, tt.args.realizedType, tt.args.relatedType, tt.args.relatedID, tt.args.attempts, tt.args.sleep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateTooManyRequests() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateServiceUnavailable(t *testing.T) {
	type args struct {
		resourceType string
		resourceID   string
		realizedType string
		relatedType  string
		relatedID    string
		attempts     string
		sleep        string
	}
	tests := []struct {
		name string
		args args
		want *ServiceUnavailable
	}{
		{"1", args{"resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep"}, &ServiceUnavailable{ServerBusyImpl{msg: fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", "resourceType", "resourceID", "realizedType", "relatedType", "relatedID", "attempts", "sleep")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateServiceUnavailable(tt.args.resourceType, tt.args.resourceID, tt.args.realizedType, tt.args.relatedType, tt.args.relatedID, tt.args.attempts, tt.args.sleep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateServiceUnavailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateClientCertificateNotTrusted(t *testing.T) {
	tests := []struct {
		name string
		want *ClientCertificateNotTrusted
	}{
		{"1", &ClientCertificateNotTrusted{managerErrorImpl{nsxErrorImpl{msg: "Certificate not trusted"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateClientCertificateNotTrusted(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateClientCertificateNotTrusted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateBadXSRFToken(t *testing.T) {
	tests := []struct {
		name string
		want *BadXSRFToken
	}{
		{"1", &BadXSRFToken{managerErrorImpl{nsxErrorImpl{msg: "Bad or expired XSRF token"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateBadXSRFToken(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateBadXSRFToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateInvalidCredentials(t *testing.T) {
	type args struct {
		msg string
	}
	tests := []struct {
		name string
		args args
		want *InvalidCredentials
	}{
		{"1", args{"msg"}, &InvalidCredentials{managerErrorImpl{nsxErrorImpl{msg: "Failed to authenticate with NSX: msg"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateInvalidCredentials(tt.args.msg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateInvalidCredentials() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateInvalidLicense(t *testing.T) {
	type args struct {
		msg string
	}
	tests := []struct {
		name string
		args args
		want *InvalidLicense
	}{
		{"1", args{"msg"}, &InvalidLicense{managerErrorImpl{nsxErrorImpl{msg: "No valid License to configure NSX resources: msg"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateInvalidLicense(tt.args.msg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateInvalidLicense() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateBadJSONWebTokenProviderRequest(t *testing.T) {
	type args struct {
		msg string
	}
	tests := []struct {
		name string
		args args
		want *BadJSONWebTokenProviderRequest
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateBadJSONWebTokenProviderRequest(tt.args.msg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateBadJSONWebTokenProviderRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateServiceClusterUnavailable(t *testing.T) {
	type args struct {
		clusterID string
	}
	tests := []struct {
		name string
		args args
		want *ServiceClusterUnavailable
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateServiceClusterUnavailable(tt.args.clusterID); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateServiceClusterUnavailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNSGroupMemberNotFound(t *testing.T) {
	type args struct {
		nsgroupID string
		memberID  string
	}
	tests := []struct {
		name string
		args args
		want *NSGroupMemberNotFound
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNSGroupMemberNotFound(tt.args.nsgroupID, tt.args.memberID); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNSGroupMemberNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNSGroupIsFull(t *testing.T) {
	type args struct {
		nsgroupID string
	}
	tests := []struct {
		name string
		args args
		want *NSGroupIsFull
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNSGroupIsFull(tt.args.nsgroupID); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNSGroupIsFull() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateSecurityGroupMaximumCapacityReached(t *testing.T) {
	type args struct {
		sgID string
	}
	tests := []struct {
		name string
		args args
		want *SecurityGroupMaximumCapacityReached
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateSecurityGroupMaximumCapacityReached(tt.args.sgID); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateSecurityGroupMaximumCapacityReached() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNsxSearchInvalidQuery(t *testing.T) {
	type args struct {
		reason string
	}
	tests := []struct {
		name string
		args args
		want *NsxSearchInvalidQuery
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxSearchInvalidQuery(tt.args.reason); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxSearchInvalidQuery() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNsxSearchErrorImpl_nsxSearchError(t *testing.T) {
	type fields struct {
		nsxErrorImpl nsxErrorImpl
	}
	tests := []struct {
		name   string
		fields fields
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NsxSearchErrorImpl{
				nsxErrorImpl: tt.fields.nsxErrorImpl,
			}
			n.nsxSearchError()
		})
	}
}

func TestCreateGeneralNsxSearchError(t *testing.T) {
	tests := []struct {
		name string
		want *GeneralSearchError
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateGeneralNsxSearchError(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateGeneralNsxSearchError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNsxIndexingInProgress(t *testing.T) {
	tests := []struct {
		name string
		want *NsxIndexingInProgress
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxIndexingInProgress(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxIndexingInProgress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNsxSearchTimeout(t *testing.T) {
	tests := []struct {
		name string
		want *NsxSearchTimeout
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxSearchTimeout(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxSearchTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNsxSearchOutOfSync(t *testing.T) {
	tests := []struct {
		name string
		want *NsxSearchOutOfSync
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxSearchOutOfSync(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxSearchOutOfSync() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNsxPendingDelete(t *testing.T) {
	tests := []struct {
		name string
		want *NsxPendingDelete
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxPendingDelete(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxPendingDelete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNsxSegemntWithVM(t *testing.T) {
	tests := []struct {
		name string
		want *NsxSegemntWithVM
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxSegemntWithVM(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxSegemntWithVM() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNsxOverlapAddresses(t *testing.T) {
	type args struct {
		details string
	}
	tests := []struct {
		name string
		args args
		want *NsxOverlapAddresses
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxOverlapAddresses(tt.args.details); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxOverlapAddresses() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateNsxOverlapVlan(t *testing.T) {
	tests := []struct {
		name string
		want *NsxOverlapVlan
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateNsxOverlapVlan(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateNsxOverlapVlan() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateAPITransactionAborted(t *testing.T) {
	tests := []struct {
		name string
		want *APITransactionAborted
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateAPITransactionAborted(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateAPITransactionAborted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateCannotConnectToServer(t *testing.T) {
	tests := []struct {
		name string
		want *CannotConnectToServer
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateCannotConnectToServer(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateCannotConnectToServer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateResourceInUse(t *testing.T) {
	tests := []struct {
		name string
		want *ResourceInUse
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateResourceInUse(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateResourceInUse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateTimeout(t *testing.T) {
	type args struct {
		host string
	}
	tests := []struct {
		name string
		args args
		want *Timeout
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateTimeout(tt.args.host); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateConnectionError(t *testing.T) {
	type args struct {
		host string
	}
	tests := []struct {
		name string
		args args
		want *ConnectionError
	}{
		{"test1", args{"1.1.1.1"}, &ConnectionError{nsxErrorImpl{msg: fmt.Sprintf("Connect to %s error", "1.1.1.1")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateConnectionError(tt.args.host); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateConnectionError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPageMaxError_Error(t *testing.T) {
	type fields struct {
		Desc string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{"test1", fields{"msg"}, "msg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PageMaxError{
				Desc: tt.fields.Desc,
			}
			if got := err.Error(); got != tt.want {
				t.Errorf("PageMaxError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}
