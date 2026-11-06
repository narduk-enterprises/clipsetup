import Foundation
import Testing

struct SetupLinkTests {
    @Test func readsTheDialsQR() throws {
        let link = try #require(SetupLink(url: URL(string: "https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=k3m9qrst")))
        #expect(link.name == "PROV_DIAL_4B98")
        #expect(link.pin == "k3m9qrst")
        #expect(link.displayName == "Dial 4B98")
    }

    @Test func roundTripsThroughItsURL() throws {
        let link = try #require(SetupLink(name: "PROV_BUTTON_HUB_0838", pin: "abc&def"))
        #expect(SetupLink(url: link.url) == link)
        #expect(link.displayName == "Button Hub 0838")
    }

    @Test func refusesOtherLinks() {
        #expect(SetupLink(url: URL(string: "http://setup.nard.uk/d?n=PROV_X&p=1")) == nil)       // not https
        #expect(SetupLink(url: URL(string: "https://evil.example/d?n=PROV_X&p=1")) == nil)       // other host
        #expect(SetupLink(url: URL(string: "https://setup.nard.uk/x?n=PROV_X&p=1")) == nil)      // other path
        #expect(SetupLink(url: URL(string: "https://setup.nard.uk/d?n=SPEAKER&p=1")) == nil)     // not a setup device
        #expect(SetupLink(url: URL(string: "https://setup.nard.uk/d?n=PROV_DIAL_4B98")) == nil)  // no PIN
        #expect(SetupLink(url: nil) == nil)
    }
}
