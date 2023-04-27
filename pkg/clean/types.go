package clean

type cleanup interface {
	Cleanup() error
}
