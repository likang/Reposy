# Reposy

Reposy is a Go CLI tool that synchronizes local repository folders with Amazon S3 storage, allowing for easy backup and version control integration.


## Why Reposy?

While solutions like GitHub and Dropbox exist for file synchronization and version control, Reposy fills a gap:

### Compared to GitHub
- GitHub only tracks committed files in your repository
- Reposy handles both tracked AND untracked files that aren't in `.gitignore`
- Perfect for backing up work-in-progress changes that aren't ready for commit

### Compared to Dropbox
- Dropbox synchronizes everything in its folder
- Reposy respects `.gitignore` rules, avoiding unnecessary syncs of:
  - Build artifacts
  - Dependencies
  - Local configuration files
  - Temporary files


## Limitations

While Reposy is a powerful tool for repository synchronization, there are some current limitations to be aware of:

### Multi-device Synchronization
- Reposy supports syncing repositories across multiple devices, but assumes only one device is actively syncing at a time
- No built-in conflict resolution when multiple devices attempt to sync simultaneously
- Users should coordinate device usage to avoid potential conflicts

### Deletion Tracking
- File deletions are only detected after Reposy is launched
- Files deleted while Reposy was not running will not be synchronized to other devices
- For complete deletion sync, ensure Reposy is running when files are deleted


## Installation

### Prerequisites

- Git (needed for the `git ls-files` command)
- Credentials with AWS S3 access or other S3-compatible services (Google Cloud Storage, etc.)

### Building from source

```bash
# Clone the repository
git clone https://github.com/likang/reposy.git
cd reposy

# Build the binary
go build -o reposy

# Install to your PATH (optional)
mv reposy /usr/local/bin/
```

## Configuration

Create a configuration file at `~/.config/reposy.json` with the following structure:

```json
{
  "version": 1,
  "repositories": {
    "/home/project1": {
        "type": "s3",
        "prefix": "project1/",
        "endpoint": "",
        "bucket": "my-projects-bucket",
        "region": "us-west-2",
        "access_key_id": "YOUR_ACCESS_KEY",
        "secret_access_key": "YOUR_SECRET_KEY"
    },
    "/home/project2": {
        "type": "s3",
        "prefix": "project2/",
    }
  },
  "s3": {
    "endpoint": "",
    "bucket": "default-bucket",
    "region": "us-east-1",
    "access_key_id": "DEFAULT_ACCESS_KEY",
    "secret_access_key": "DEFAULT_SECRET_KEY"
  }
}
```


## Usage

### Basic Commands

```bash
# Start the daemon
reposy start

# Check sync status of all repositories
reposy status

# Reload configuration
reposy reload

# Stop the daemon
reposy stop
```

### How It Works

1. Lists local files using `git ls-files --others --exclude-standard --cached`
2. Compares modification times with the remote to determine which files need to be updated
3. Uploads, updates, or deletes files as needed


## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.