# GitKraken Enhancer

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

#### An open-source interoperability tool designed to extend local client-side configurations and improve the UX of the GitKraken application.

## Overview

GitKraken Enhancer is a lightweight desktop utility that adjusts local client settings to unlock additional interface features and workflow capabilities. All modifications are performed locally on your machine — no network requests are made, and no server-side validations are involved.

## Features

- Auto-detect GitKraken installations across multiple directories
- One-click configuration application
- Clean, modern light-themed UI
- Supports multiple GitKraken versions (10.6.0 - 12.3.1)
- Automatic backup of original `app.asar` before applying changes
- Custom asar file selection for non-standard installations

## Supported Versions

| GitKraken Version | Status |
|------------------|--------|
| 12.3.1           | Supported |
| 12.3.0           | Supported |
| 12.2.0           | Supported |
| 12.1.2           | Supported |
| 12.1.1           | Supported |
| 12.1.0           | Supported |
| 11.4.0           | Supported |
| 11.2.1           | Supported |
| 11.1.1           | Supported |
| 11.1.0           | Supported |
| 11.0.0           | Supported |
| 10.8.1           | Supported |
| 10.8.0           | Supported |
| 10.7.0           | Supported |
| 10.6.3           | Supported |
| 10.6.2           | Supported |
| 10.6.1           | Supported |
| 10.6.0           | Supported |

## Installation Detection Paths

### Windows
- `%LOCALAPPDATA%\gitkraken\app-*\resources\app.asar`
- `%ProgramData%\gitkraken\app-*\resources\app.asar`
- `%ProgramData%\%USERNAME%\gitkraken\app-*\resources\app.asar`

### macOS
- `/Applications/GitKraken.app/Contents/Resources/app.asar`

## Requirements

- **Node.js** (v18+) - required for asar extraction/repackaging
- **GitKraken** installed on your system

## Usage

1. Download the latest release or build from source
2. Run the application
3. The app will auto-scan for GitKraken installations
4. Select the version you want to apply the configuration to
5. Click **Apply**
6. Restart GitKraken to see the changes

## Build from Source

```bash
# Install Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Build
wails build

# Copy runtime dependencies
cp asar-helper.mjs build/bin/
cp -r patches build/bin/
cp -r node_modules/@electron build/bin/node_modules/
```

## How It Works

This tool modifies local client bundle files to extend the licensed features configuration array, enabling additional interface elements and workflow options that are normally gated by local feature flags.

## Q&A

- **Does this send data anywhere?**
  - No. All operations are strictly offline and local to your machine.
- **I applied the configuration but GitKraken won't start**
  - Close the application completely and restart it. If issues persist, restore the `.old` backup file.

## Legal Disclaimer

This project is a third-party enhancement tool intended solely for educational, research, and local interoperability purposes. It does not distribute any proprietary code or bypass server-side validations. All modifications are performed locally to customize the user's interface.

## Author

Dao Nguyen Hoang

## Credits

Based on the original [GitKraked](https://github.com/101123/GitKraked) project.
