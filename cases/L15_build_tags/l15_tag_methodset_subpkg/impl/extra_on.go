//go:build tagExtra

package impl

import "l15_tag_methodset_subpkg/iface"

var _ iface.Extra = (*Record)(nil)

func (r *Record) Extra() string { return "extra:" + r.P.Name }
