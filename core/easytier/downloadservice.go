package easytier

type EasyTierDownloadService struct{}

func (s *EasyTierDownloadService) Ensure() error {
	return EnsureEasyTier()
}
