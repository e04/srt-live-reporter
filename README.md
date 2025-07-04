# srt-live-reporter

```
                 ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
  SRT Stream ━━━▷┃        srt-live-reporter        ┃━━▷ UDP or SRT Stream
                 ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
                                  ╎
                                  ╎  Web Socket
                                  ╎
                                  ▽
                 ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
                 ┃ Stats                          ┃
                 ┃  ┏━━━━━━━━━━━━━━━━━━━━━━━━━━┓  ┃
                 ┃  ┃ 1 {                      ┃  ┃
                 ┃  ┃ 2   MbpsRecvRate: 10,    ┃  ┃
                 ┃  ┃ 3   MsRTT: 3,            ┃  ┃
                 ┃  ┃ 4   PktRecvLossRate: 0,  ┃  ┃
                 ┃  ┃ 5   ....                 ┃  ┃
                 ┃  ┗━━━━━━━━━━━━━━━━━━━━━━━━━━┛  ┃
                 ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
```

A lightweight Go application for receiving and relaying SRT (Secure Reliable Transport) live streams with real-time statistics reporting via WebSocket.

## The `go-irl` Stack

`srt-live-reporter` is a core component of **[go-irl](https://github.com/e04/go-irl)**, a complete, modern streaming stack designed for robust IRL broadcasting.

If you are looking for an easier setup with more advanced features, consider using `go-irl`. It bundles `srt-live-reporter` with other essential tools (`obs-srt-bridge`, `go-srtla`) and provides a simple, one-command launcher.

## Usage

### Basic Command

```bash
./srt-live-reporter -from <source> -to <destination> [-wsport <port>]
```

### Parameters

- `-from`: Source address (srt://, udp://, or `-` for stdin)
- `-to`: Destination address (srt://, udp://, file://, or `-` for stdout)
- `-wsport`: WebSocket server port for statistics (default: 8888, 0 to disable)

### Examples

```bash
./srt-live-reporter -from "srt://:5001?mode=listener" -to "udp://:5002" -wsport 8888
```

## WebSocket Statistics

When WebSocket is enabled, connect to `ws://localhost:<wsport>/ws` to receive real-time statistics in JSON format:

```typescript
interface Statistics {
  MsTimeStamp: number;
  Accumulated: {
    PktSent: number;
    PktRecv: number;
    PktSentUnique: number;
    PktRecvUnique: number;
    PktSendLoss: number;
    PktRecvLoss: number;
    PktRetrans: number;
    PktRecvRetrans: number;
    PktSentACK: number;
    PktRecvACK: number;
    PktSentNAK: number;
    PktRecvNAK: number;
    PktSentKM: number;
    PktRecvKM: number;
    UsSndDuration: number;
    PktRecvBelated: number;
    PktSendDrop: number;
    PktRecvDrop: number;
    PktRecvUndecrypt: number;

    ByteSent: number;
    ByteRecv: number;
    ByteSentUnique: number;
    ByteRecvUnique: number;
    ByteRecvLoss: number;
    ByteRetrans: number;
    ByteRecvRetrans: number;
    ByteRecvBelated: number;
    ByteSendDrop: number;
    ByteRecvDrop: number;
    ByteRecvUndecrypt: number;
  };

  Interval: {
    MsInterval: number;

    PktSent: number;
    PktRecv: number;
    PktSentUnique: number;
    PktRecvUnique: number;
    PktSendLoss: number;
    PktRecvLoss: number;
    PktRetrans: number;
    PktRecvRetrans: number;
    PktSentACK: number;
    PktRecvACK: number;
    PktSentNAK: number;
    PktRecvNAK: number;

    MbpsSendRate: number;
    MbpsRecvRate: number;

    UsSndDuration: number;

    PktReorderDistance: number;
    PktRecvBelated: number;
    PktSndDrop: number;
    PktRecvDrop: number;
    PktRecvUndecrypt: number;

    ByteSent: number;
    ByteRecv: number;
    ByteSentUnique: number;
    ByteRecvUnique: number;
    ByteRecvLoss: number;
    ByteRetrans: number;
    ByteRecvRetrans: number;
    ByteRecvBelated: number;
    ByteSendDrop: number;
    ByteRecvDrop: number;
    ByteRecvUndecrypt: number;
  };

  Instantaneous: {
    UsPktSendPeriod: number;
    PktFlowWindow: number;
    PktFlightSize: number;
    MsRTT: number;
    MbpsSentRate: number;
    MbpsRecvRate: number;
    MbpsLinkCapacity: number;
    ByteAvailSendBuf: number;
    ByteAvailRecvBuf: number;
    MbpsMaxBW: number;
    ByteMSS: number;
    PktSendBuf: number;
    ByteSendBuf: number;
    MsSendBuf: number;
    MsSendTsbPdDelay: number;
    PktRecvBuf: number;
    ByteRecvBuf: number;
    MsRecvBuf: number;
    MsRecvTsbPdDelay: number;
    PktReorderTolerance: number;
    PktRecvAvgBelatedTime: number;
    PktSendLossRate: number;
    PktRecvLossRate: number;
  };
}

interface WebSocketMessage {
  timestamp: string;
  type: "reader" | "writer";
  stats: Statistics;
}
```

For detailed information about the statistics fields, refer to the [gosrt statistics code](https://github.com/datarhei/gosrt/blob/main/statistics.go).

### Viewing Statistics with wscat

To view real-time statistics in the console, you can use `wscat`:

```bash
npm install -g wscat
wscat -c ws://localhost:8888/ws
```

## Dependencies

- [github.com/datarhei/gosrt](https://github.com/datarhei/gosrt) - SRT protocol implementation
  - The implementation is based on the [gosrt client sample application](https://github.com/datarhei/gosrt/tree/main/contrib/client).
- [github.com/gorilla/websocket](https://github.com/gorilla/websocket) - WebSocket support
