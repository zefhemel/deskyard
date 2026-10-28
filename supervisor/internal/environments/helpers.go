package environments

import (
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// Helpers is the runner of this supervisor's helper containers, made on
// first use from Eng and Instance. Storage shares it, so stale helpers are
// removed once and never while one runs.
func (s *Service) Helpers() *helper.Runner {
	s.helpersOnce.Do(func() { s.helpers = &helper.Runner{Eng: s.Eng, Instance: s.Instance} })
	return s.helpers
}
