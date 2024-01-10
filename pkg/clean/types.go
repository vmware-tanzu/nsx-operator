package clean

type cleanup interface {
	Cleanup() error
}

type cleanupFunc func() (cleanup, error)

type CleanupService struct {
	cleans []cleanup
	err    error
}

func NewCleanupService() *CleanupService {
	return &CleanupService{}
}

func (c *CleanupService) AddCleanupService(f cleanupFunc) *CleanupService {
	var clean cleanup
	if c.err != nil {
		return c
	}

	clean, c.err = f()
	if c.err != nil {
		return c
	}

	c.cleans = append(c.cleans, clean)
	return c
}

type Status struct {
	Code    uint32
	Message string
}

var (
	OK                       Status = Status{Code: 0, Message: "cleanup successfully"}
	ValidationFailed         Status = Status{Code: 1, Message: "failed to validate config"}
	GetNSXClientFailed       Status = Status{Code: 2, Message: "failed to get nsx client"}
	InitCleanupServiceFailed Status = Status{Code: 3, Message: "failed to initialize cleanup service"}
	CleanupResourceFailed    Status = Status{Code: 4, Message: "failed to clean up"}
)
