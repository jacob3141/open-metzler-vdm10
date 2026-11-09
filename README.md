# open-metzler-vdm10

Local software for the Metzler VDM10-VM-POE video door station. It needs no cloud service.

## Components

| Path | Purpose |
|---|---|
| `device/doorbelld` | WebSocket server on the door station: JSON-RPC, button events, video |
| `device/mediad` | H.264 video pipeline on the door station |
| `hub` | Docker service: client registration, ring handling, visitor messages |
| `hub/cmd/stubclient` | Command-line stand-in for the planned Android app |
| `viewer` | Browser page that shows the live video |
| `tools` | Small test clients |

## Status

Video and the button event work. Audio does not work yet, so visitor messages are silent. The Docker image has not been built yet.

`doorbelld` has no authentication, so keep the door station on a trusted network. The hub requires an admin password and per-client tokens.

## Running the hub

```sh
cd hub
cp .env.example .env   # then fill in the values
docker compose up -d --build
```

The web interface is on port 8088.

## Client API

Send `Authorization: Bearer <token>` with every request. Create tokens in the web interface.

| Endpoint | Purpose |
|---|---|
| `ws /api/events` | JSON-RPC 2.0 |
| `ws /api/video` | One binary message per frame, H.264 Annex B, starting at the next keyframe |
| `GET /api/messages` | List visitor messages as JSON |
| `GET /api/messages/{id}` | Download a visitor message as MP4 |

The hub sends the notifications `ring {id, time}`, `ring.taken {id, by}`, `ring.missed {id}` and `message.new {id, time}`. Clients call `ring.accept {id}`, `ring.reject {id}` and `system.ping`.

## License

AGPL-3.0-or-later, see [LICENSE](LICENSE).
