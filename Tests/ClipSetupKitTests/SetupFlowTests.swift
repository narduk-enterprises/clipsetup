import ClipSetupKit
import Foundation
import Testing

/// Plays the device: answers each effect the flow asks for, and records the order they came in.
private struct FakeDevice {
    var flow: SetupFlow
    var effects: [SetupFlow.Effect] = []
    var replies: [String: SetupFlow.Event] = [:]   // endpoint → how the device answers it
    var networks = [SetupNetwork(ssid: "Tideye", rssi: -50, secured: true)]

    init(steps: [CustomStep]) { flow = SetupFlow(displayName: "Mybo 4B98", steps: steps) }

    mutating func send(_ event: SetupFlow.Event) {
        var next: SetupFlow.Event? = event
        while let event = next {
            next = nil
            guard let effect = flow.handle(event) else { return }
            effects.append(effect)
            switch effect {
            case .connect: next = .connected
            case .send(let path, let data): next = replies[path] ?? .replied(Data("ok:".utf8) + data)
            case .scan: next = .scanned(networks, nil)
            case .provision(let ssid, _): next = .joined(ssid: ssid)
            }
        }
    }

    /// Start, find, connect: everything up to the network list.
    mutating func connect() {
        send(.start)
        send(.searching)
        send(.found)
    }
}

struct SetupFlowTests {
    static let token = Data("claim-token".utf8)

    @Test func withoutStepsItConnectsScansAndJoinsAsBefore() {
        var device = FakeDevice(steps: [])
        device.connect()
        #expect(device.flow.state == .pickNetwork)
        device.send(.join(ssid: "Tideye", password: "pw"))
        #expect(device.effects == [.connect, .scan, .provision(ssid: "Tideye", password: "pw")])
        #expect(device.flow.state == .done(ssid: "Tideye"))
    }

    @Test func customStepsRunInOrderAfterTheSessionOpensAndBeforeTheWifi() {
        var device = FakeDevice(steps: [.custom(path: "mybo-claim", data: Self.token), .custom(path: "mybo-name", data: Data("Tideye".utf8))])
        device.connect()
        device.send(.join(ssid: "Tideye", password: "pw"))
        #expect(device.effects == [
            .connect,
            .send(path: "mybo-claim", data: Self.token),
            .send(path: "mybo-name", data: Data("Tideye".utf8)),
            .scan,
            .provision(ssid: "Tideye", password: "pw"),
        ])
        #expect(device.flow.replies["mybo-claim"] == Data("ok:".utf8) + Self.token)
        #expect(device.flow.state == .done(ssid: "Tideye"))
    }

    @Test func aFailedStepStopsSetupBeforeTheWifi() {
        var device = FakeDevice(steps: [.custom(path: "mybo-claim", data: Self.token), .custom(path: "mybo-name", data: Data())])
        device.replies["mybo-claim"] = .customFailed("sendDataError")
        device.connect()
        device.send(.join(ssid: "Tideye", password: "pw"))
        device.send(.rescan)
        #expect(device.effects == [.connect, .send(path: "mybo-claim", data: Self.token)])
        #expect(device.flow.state == .failed("Mybo 4B98 didn't take mybo-claim: sendDataError"))
    }

    @Test func aStepsCheckCanRefuseTheReply() {
        let step = CustomStep.custom(path: "mybo-claim", data: Self.token) { reply in
            reply == Data("ok:".utf8) + Data("claim-token".utf8) ? "This box already has an owner." : nil
        }
        var device = FakeDevice(steps: [step])
        device.connect()
        device.send(.join(ssid: "Tideye", password: "pw"))
        #expect(device.effects == [.connect, .send(path: "mybo-claim", data: Self.token)])
        #expect(device.flow.state == .failed("This box already has an owner."))
    }

    @Test func aDropDuringAStepIsAFailureButADropWhileJoiningIsNot() {
        var flow = SetupFlow(displayName: "Mybo 4B98", steps: [.custom(path: "mybo-claim", data: Self.token)])
        _ = flow.handle(.found)
        #expect(flow.handle(.connected) == .send(path: "mybo-claim", data: Self.token))
        #expect(flow.state == .custom(path: "mybo-claim"))
        _ = flow.handle(.disconnected)
        #expect(flow.state == .failed("Mybo 4B98 disconnected. Try again."))

        var joining = FakeDevice(steps: [])
        joining.connect()
        _ = joining.flow.handle(.join(ssid: "Tideye", password: "pw"))
        _ = joining.flow.handle(.disconnected)
        #expect(joining.flow.state == .sending(ssid: "Tideye"))
    }

    @Test func wifiCannotJumpAheadOfTheSteps() {
        var flow = SetupFlow(displayName: "Mybo 4B98", steps: [.custom(path: "mybo-claim", data: Self.token)])
        _ = flow.handle(.found)
        _ = flow.handle(.connected)
        #expect(!flow.stepsDone)
        #expect(flow.handle(.join(ssid: "Tideye", password: "pw")) == nil)
        #expect(flow.handle(.rescan) == nil)
        #expect(flow.state == .custom(path: "mybo-claim"))
        #expect(flow.handle(.replied(Data())) == .scan)
        #expect(flow.stepsDone)
    }

    @Test func lateOrRepeatedCallbacksDoNotAdvanceTheFlow() {
        var flow = SetupFlow(displayName: "Mybo 4B98", steps: [.custom(path: "mybo-claim", data: Self.token)])
        _ = flow.handle(.found)
        #expect(flow.handle(.connected) == .send(path: "mybo-claim", data: Self.token))
        #expect(flow.handle(.connected) == nil)                       // a repeat never resends the token
        #expect(flow.handle(.scanned([SetupNetwork(ssid: "Old", rssi: -40, secured: true)], nil)) == nil)
        #expect(flow.networks.isEmpty)
        _ = flow.handle(.joined(ssid: "Old"))                         // a provision reply from an earlier try
        #expect(flow.state == .custom(path: "mybo-claim"))
    }

    @Test func tryingAgainRunsTheStepsAgainInTheNewSession() {
        var device = FakeDevice(steps: [.custom(path: "mybo-claim", data: Self.token)])
        device.connect()
        device.connect()
        #expect(device.effects == [.connect, .send(path: "mybo-claim", data: Self.token), .scan,
                                   .connect, .send(path: "mybo-claim", data: Self.token), .scan])
    }

    @Test func espressifsOwnEndpointsAreRefused() {
        for path in ["prov-config", "prov-session", "proto-ver", "prov-scan", "prov-ctrl", ""] {
            var device = FakeDevice(steps: [.custom(path: path, data: Self.token)])
            device.connect()
            #expect(device.effects == [.connect])
            guard case .failed = device.flow.state else { Issue.record("\(path) was not refused"); continue }
        }
    }

    @Test func failuresReadAsBefore() {
        var flow = SetupFlow(displayName: "Dial 4B98")
        _ = flow.handle(.notFound(nil))
        #expect(flow.state == .failed("Couldn't find Dial 4B98. Make sure it's showing its setup code and your phone is close. (not found)"))
        _ = flow.handle(.connectFailed("Security error: wrong pop"))
        #expect(flow.state == .failed("Dial 4B98 didn't accept the code. Show a fresh setup code on it and scan again."))
        _ = flow.handle(.connectFailed("timed out"))
        #expect(flow.state == .failed("Couldn't connect to Dial 4B98: timed out"))
        _ = flow.handle(.cancelled)
        #expect(flow.state == .failed("Setup was cancelled. Tap Try again when you're ready."))
        _ = flow.handle(.found)
        _ = flow.handle(.connected)
        _ = flow.handle(.scanned([], "busy"))
        #expect(flow.state == .failed("Dial 4B98 didn't see any Wi-Fi. busy"))
    }

    @Test func networksAreStrongestFirstOnePerNameAndNoBlanks() {
        var flow = SetupFlow(displayName: "Dial 4B98")
        _ = flow.handle(.found)
        _ = flow.handle(.connected)
        _ = flow.handle(.scanned([
            SetupNetwork(ssid: "Mesh", rssi: -80, secured: true),
            SetupNetwork(ssid: "", rssi: -40, secured: false),
            SetupNetwork(ssid: "Cafe", rssi: -60, secured: false),
            SetupNetwork(ssid: "Mesh", rssi: -50, secured: true),
        ], nil))
        #expect(flow.networks.map(\.ssid) == ["Mesh", "Cafe"])
        #expect(flow.networks.map(\.bars) == [3, 2])
        #expect(flow.state == .pickNetwork)
    }

    @Test func configAppliedKeepsSendingUntilJoined() {
        var flow = SetupFlow(displayName: "Dial 4B98")
        _ = flow.handle(.found)
        _ = flow.handle(.connected)
        #expect(flow.handle(.join(ssid: "Cafe", password: "")) == .provision(ssid: "Cafe", password: ""))
        _ = flow.handle(.configApplied(ssid: "Cafe"))
        #expect(flow.state == .sending(ssid: "Cafe"))
        _ = flow.handle(.joinFailed("That password didn't work for Cafe."))
        #expect(flow.state == .failed("That password didn't work for Cafe."))
    }
}
