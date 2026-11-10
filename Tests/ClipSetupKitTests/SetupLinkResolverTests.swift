import ClipSetupKit
import Foundation
import Testing

struct SetupLinkResolverTests {
    static let box = SetupLinkForm.code(host: "mybo.at", pathPrefix: "/box/", deviceName: "PROV_MYBO_")
    static let myBoat = SetupLinkResolver.clipSetup(plus: [box])

    @Test func clipSetupAloneReadsOnlyItsOwnForm() throws {
        let link = try #require(SetupLinkResolver.clipSetup.resolve(URL(string: "https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=k3m9qrst")))
        #expect(link == SetupLink(name: "PROV_DIAL_4B98", pin: "k3m9qrst"))
        #expect(SetupLinkResolver.clipSetup.resolve(URL(string: "https://mybo.at/box/k3m9qrst2wxv")) == nil)
        #expect(SetupLinkResolver.clipSetup.resolve(nil) == nil)
    }

    @Test func aHostFormAndTheClipSetupFormBothResolve() throws {
        let box = try #require(Self.myBoat.resolve(URL(string: "https://mybo.at/box/k3m9qrst2wxv")))
        #expect(box.name == "PROV_MYBO_")
        #expect(box.pin == "k3m9qrst2wxv")
        #expect(box.displayName == "Mybo")
        let dial = try #require(Self.myBoat.resolve(URL(string: "https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=k3m9qrst")))
        #expect(dial.name == "PROV_DIAL_4B98")
    }

    @Test func theCodeIgnoresDashesCaseAndATrailingSlash() {
        let want = SetupLink(name: "PROV_MYBO_", pin: "k3m9qrst2wxv")
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at/box/k3m9-qrst-2wxv")) == want)
        #expect(Self.myBoat.resolve(URL(string: "https://MyBo.At/box/K3M9-QRST-2WXV/")) == want)
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at/box/k3m9qrst2wxv?utm=x#top")) == want)
    }

    @Test func refusesLinksThatAreNotTheHostForm() {
        #expect(Self.myBoat.resolve(URL(string: "http://mybo.at/box/k3m9qrst2wxv")) == nil)        // not https
        #expect(Self.myBoat.resolve(URL(string: "https://evil.example/box/k3m9qrst2wxv")) == nil)  // other host
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at.evil.example/box/k3m9")) == nil)  // host suffix trick
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at:8443/box/k3m9qrst2wxv")) == nil)  // a port
        #expect(Self.myBoat.resolve(URL(string: "https://x@mybo.at/box/k3m9qrst2wxv")) == nil)    // a user
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at/box/")) == nil)                   // no code
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at/box/a/b")) == nil)                // extra segment
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at/boxes/k3m9")) == nil)             // other path
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at/chart")) == nil)                  // the app's other links
        #expect(Self.myBoat.resolve(URL(string: "https://mybo.at/box/" + String(repeating: "a", count: 65))) == nil) // PIN too long
    }

    @Test func aPrefixWithoutItsSlashStillMatchesWholeSegments() {
        let form = SetupLinkForm.code(host: "mybo.at", pathPrefix: "/box", deviceName: "PROV_MYBO_")
        #expect(form.link(from: URL(string: "https://mybo.at/box/k3m9")!) == SetupLink(name: "PROV_MYBO_", pin: "k3m9"))
        #expect(form.link(from: URL(string: "https://mybo.at/boxk3m9")!) == nil)
    }

    @Test func formsAreTriedInOrder() {
        let first = SetupLinkForm { _ in SetupLink(name: "PROV_FIRST", pin: "1") }
        let second = SetupLinkForm { _ in SetupLink(name: "PROV_SECOND", pin: "2") }
        #expect(SetupLinkResolver([first, second]).resolve(URL(string: "https://anything.example"))?.name == "PROV_FIRST")
        #expect(SetupLinkResolver([]).resolve(URL(string: "https://setup.nard.uk/d?n=PROV_X&p=1")) == nil)
    }
}
