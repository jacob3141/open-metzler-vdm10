# Doorbell app for Android

Client app for the doorbell hub. It only works in the home Wi-Fi, there is no cloud service.

- **Pairing:** the app finds the hub in the Wi-Fi by mDNS (or you type its name, e.g. `doorbell-hub.home.arpa`; the port 8088 is the default) and asks for access. Approve the phone in the web interface of the hub under "Devices asking for access" if the code is the same as on the phone. Alternatively, enter the token of a client created in the web interface.
- **Ringing:** a foreground service keeps the connection to the hub while the phone is in Wi-Fi. A ring shows the door camera in full screen, also on the lock screen, and rings like a phone call. "Answer" tells the hub that someone takes care of it, "Decline" lets the hub record a visitor message once everyone declined.
- **Live view** of the door camera, and the list of **visitor messages**.
- Sound from the door station plays in the live view and once a ring is answered, not while it rings. Hold "Hold to talk" to talk to the door; the sound of the door is muted meanwhile, so it does not echo.
- Visitor messages have video and sound.
- English, with a German translation.

## Installing

Open the web interface of the hub and scan the QR code under "Android app" with the phone, or open `http://doorbell-hub.home.arpa:8088/app.apk` on it. The first time, Android asks to allow installing apps from the browser. For an update, open the link again: the app stays paired.

GitHub Actions builds the app with every push and puts it into the Docker image of the hub, signed with the release key from the secrets `DOORBELL_KEYSTORE_B64` (the keystore in base64, alias `doorbell`) and `DOORBELL_KEYSTORE_PASSWORD`. Android only updates an app signed with the same key, so keep the key: with a new one the app has to be uninstalled and paired again.

Android 10 (API 29) or newer. On Android 14 and newer, allow full-screen notifications when the app asks, otherwise a ring on a locked phone only shows a notification. Exempt the app from battery optimization so that it stays connected.

## Building

Needs JDK 17 or newer and the Android SDK (platform 35). Android Studio brings both; open this folder there. On the command line:

```sh
echo "sdk.dir=/path/to/Android/Sdk" > local.properties
./gradlew testDebugUnitTest assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

A debug build is signed with the debug key of your machine, so it does not update an app installed from the hub. To build the release, set `DOORBELL_KEYSTORE` (path to the keystore) and `DOORBELL_KEYSTORE_PASSWORD`, then run `./gradlew assembleRelease`.

## How it works

| File | Purpose |
|---|---|
| `DoorbellService.kt` | Foreground service with the JSON-RPC connection (`/api/events`), reconnects with backoff, turns rings into notifications |
| `WifiNet.kt` | Tracks the Wi-Fi network; all connections go through it, never through mobile data |
| `CallActivity.kt` | Door view: during a ring with Answer and Decline, or as live view |
| `Talker.kt` | Push-to-talk: records the phone microphone and sends it to `/api/talk` |
| `PcmPlayer.kt` | Plays the sound of `/api/audio` (16 kHz PCM) with `AudioTrack`, at most 250 ms behind |
| `H264Player.kt`, `AnnexB.kt` | Decodes the H.264 frames of `/api/video` with `MediaCodec` onto a surface, dropping to the next keyframe when it falls behind |
| `SetupActivity.kt` | Pairing: mDNS discovery, access request with code, or token |
| `MessagesActivity.kt`, `PlayerActivity.kt` | Visitor messages, downloaded and played from the cache |

## Testing without pressing the real button

`hub/cmd/fakedoorbell` stands in for the door station: it relays video and sound of the real one and rings on `POST /ring`.

```sh
cd hub
go run ./cmd/fakedoorbell -listen localhost:8090 -video doorbell.home.arpa:8080 &
HUB_LISTEN=localhost:8088 HUB_DOORBELL=localhost:8090 HUB_DATA=/tmp/hub HUB_MDNS=off go run . &
curl -X POST localhost:8090/ring
```

In the Android emulator the hub on the computer is `10.0.2.2:8088`.
