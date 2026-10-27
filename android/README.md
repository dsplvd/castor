# Castor for Android

A remote control for a castor API server: pick a TV, cast a page or a stream URL, watch its state, stop it.
Kotlin, Jetpack Compose (Material 3), connect-kotlin on OkHttp (Connect protocol, bearer token).

## Build and install

```sh
export ANDROID_HOME=$HOME/Library/Android/sdk   # and a JDK 17
echo "sdk.dir=$ANDROID_HOME" > local.properties # gitignored
./gradlew :app:assembleDebug :app:testDebugUnitTest
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

## Regenerate the protobuf and Connect code

`app/src/main/gen/` is generated from `../proto/castor/v1` and committed; never edit it by hand.
Run from `android/` after changing `proto/` (needs network for the Buf remote plugins):

```sh
go run github.com/bufbuild/buf/cmd/buf@v1.50.0 generate --template buf.gen.yaml
```

The generated Java targets protobuf-javalite 4.36.2; keep `protobuf` in `gradle/libs.versions.toml`
at least that version when the plugins are updated (see the header of the generated files).

## Use

1. Settings: server URL (e.g. `http://192.168.50.32:8411`), token, optional max height and delivery; "Test connection" calls ListDevices.
2. Devices: pull to refresh, tap one to make it the default, or pin a type and address for a TV the server cannot discover.
3. Cast: paste a URL, choose page (server extracts the stream) or direct stream URL, Cast.
   Share a page from Chrome to Castor to open this screen prefilled.
4. Now Playing follows the cast; the stream drops in the background and reconnects on return.

Opening http(s) links with Castor is off by default. To enable it:
`adb shell pm enable dev.castor.android/.ViewLinksAlias`.

Cleartext HTTP is allowed (see `res/xml/network_security_config.xml`) because the server is on the LAN without TLS.
