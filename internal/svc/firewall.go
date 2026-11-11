package svc

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// nftAvailable reports whether the nft binary is usable.
func (m *Manager) nftAvailable() bool { return m.Available("nft") }

// nftApply deletes a table (if present) and applies a script atomically.
func (m *Manager) nftApply(ctx context.Context, table, script string) error {
	if !m.nftAvailable() {
		return fmt.Errorf("未安装 nftables（nft）")
	}
	_, _ = m.Run(ctx, 5*time.Second, "nft", "delete", "table", "inet", table)
	if _, err := m.RunInput(ctx, 15*time.Second, script, "nft", "-f", "-"); err != nil {
		return fmt.Errorf("应用防火墙规则失败: %w", err)
	}
	return nil
}

func (m *Manager) nftDelete(ctx context.Context, table string) {
	if !m.nftAvailable() {
		return
	}
	_, _ = m.Run(ctx, 5*time.Second, "nft", "delete", "table", "inet", table)
}

// applyBridgeNAT allows forwarding between the AP and upstream interfaces and
// masquerades traffic leaving the upstream interface.
func (m *Manager) applyBridgeNAT(ctx context.Context, apIface, wanIface string) error {
	script := fmt.Sprintf(`table inet %s {
  chain forward {
    type filter hook forward priority filter; policy accept;
    iifname %q oifname %q accept
    iifname %q oifname %q ct state established,related accept
  }
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    oifname %q masquerade
  }
}
`, nftBridgeTable, apIface, wanIface, wanIface, apIface, wanIface)
	return m.nftApply(ctx, nftBridgeTable, script)
}

// applyAPNAT is the NAT rule set for a standalone access point: masquerade
// everything leaving the WAN interface while the AP is a different device.
func (m *Manager) applyAPNAT(ctx context.Context, apIface, wanIface string) error {
	script := fmt.Sprintf(`table inet %s {
  chain forward {
    type filter hook forward priority filter; policy accept;
    iifname %q oifname %q accept
    iifname %q oifname %q ct state established,related accept
  }
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    oifname %q masquerade
  }
}
`, nftBridgeTable, apIface, wanIface, wanIface, apIface, wanIface)
	return m.nftApply(ctx, nftBridgeTable, script)
}

// syncBlockedSet rebuilds the MAC filter set used by nftables.
func (m *Manager) syncBlockedSet(ctx context.Context) error {
	if !m.nftAvailable() {
		return nil
	}
	blocked := m.store.Blocked()
	script := fmt.Sprintf(`table inet %s {
  set blocked { type ether_addr; }
  chain forward {
    type filter hook forward priority filter + 5; policy accept;
    ether saddr @blocked drop;
  }
}
`, nftFilterTable)
	if err := m.nftApply(ctx, nftFilterTable, script); err != nil {
		return err
	}
	for _, mac := range blocked {
		if err := m.addBlockedElement(ctx, mac); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) addBlockedElement(ctx context.Context, mac string) error {
	_, err := m.Run(ctx, 5*time.Second, "nft", "add", "element", "inet", nftFilterTable, "blocked", "{", mac, "}")
	if err != nil && !strings.Contains(err.Error(), "File exists") {
		return err
	}
	return nil
}

func (m *Manager) delBlockedElement(ctx context.Context, mac string) error {
	_, err := m.Run(ctx, 5*time.Second, "nft", "delete", "element", "inet", nftFilterTable, "blocked", "{", mac, "}")
	if err != nil && !strings.Contains(err.Error(), "No such file") && !strings.Contains(err.Error(), "does not exist") {
		return err
	}
	return nil
}
