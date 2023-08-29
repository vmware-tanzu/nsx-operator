package clean

import "context"

type cleanup interface {
	Cleanup(ctx context.Context) error
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
