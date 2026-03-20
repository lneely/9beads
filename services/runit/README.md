# runit Service for 9beads

## Installation

```sh
sudo cp -r services/runit /etc/sv/9beads
sudo chmod +x /etc/sv/9beads/run
sudo ln -s /etc/sv/9beads /var/service/9beads
```

## Management

- Start: `sudo sv start 9beads`
- Stop: `sudo sv stop 9beads`
- Restart: `sudo sv restart 9beads`
- Status: `sudo sv status 9beads`
