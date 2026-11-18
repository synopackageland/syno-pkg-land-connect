"""The Go release that builds every binary in the SPK, as the CLI pins its own."""
import os

# Go 1.26.0 crashed at start on armv7 kernel 3.10 (DSM 7.1.1); 1.26.9 ran.
GO_TOOLCHAIN = 'go1.26.10'


def pin_go_toolchain():
    os.environ['GOTOOLCHAIN'] = GO_TOOLCHAIN
