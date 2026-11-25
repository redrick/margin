//go:build linux && !gtk3 && !gtk4

package main

// On Linux the toolkit is picked at build time: -tags gtk3 (GTK 3, WebKitGTK 4.1; use this on X11,
// where the GTK 4 build stalls while scrolling under load) or -tags gtk4 (GTK 4, WebKitGTK 6).
// See docs/gui.md#install.
var _ = margin_gui_on_linux_needs_tags_gtk3_or_gtk4
