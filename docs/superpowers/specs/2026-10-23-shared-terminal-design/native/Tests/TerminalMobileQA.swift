import XCTest
import UIKit
@MainActor
final class TerminalMobileQA: XCTestCase {
 let safari = XCUIApplication(bundleIdentifier: "com.apple.mobilesafari")
 func tap(_ element: XCUIElement) {
  XCTAssertTrue(element.waitForExistence(timeout: 15))
  safari.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: element.frame.midX,dy: element.frame.midY)).tap()
 }
 func shot(_ name: String) { let a=XCTAttachment(screenshot:XCUIScreen.main.screenshot()); a.name=name; a.lifetime = .keepAlways; add(a) }
 func testFocus() throws {
  continueAfterFailure=false; safari.activate(); sleep(3)
  tap(safari.buttons["Show keyboard"]); sleep(3)
  print(safari.debugDescription); shot("keyboard")
  XCTAssertTrue(safari.keyboards.firstMatch.exists)
 }
 func proof(_ key: String, _ expected: String) async throws {
  let base=ProcessInfo.processInfo.environment["QA_PROOF_URL"]!
  var actual="missing"
  for _ in 0..<60 {
   let (data,response)=try await URLSession.shared.data(from:URL(string:base+"/"+key)!)
   if (response as? HTTPURLResponse)?.statusCode==200 { actual=String(decoding:data,as:UTF8.self); if actual==expected { return } }
   try await Task.sleep(for:.milliseconds(250))
  }
  XCTFail("Proof \(key): expected \(expected), got \(actual)")
 }
 func keyboardVisible() {
  XCTAssertTrue(safari.keyboards.firstMatch.exists)
  // Geometry alone cannot prove visibility: review captured screenshots against Safari accessory overlays.
  let kb=safari.keyboards.firstMatch.frame
  for label in ["Show keyboard","Ctrl+C","Escape","Tab"] {
   let b=safari.buttons[label]; XCTAssertLessThanOrEqual(b.frame.maxY,kb.minY+1,"\(label) overlaps keyboard")
  }
 }
 func testNativeAcceptance() async throws {
  continueAfterFailure=false; safari.activate(); XCUIDevice.shared.orientation = .portrait; sleep(2)
  let p=ProcessInfo.processInfo.environment["QA_PROOF_PATH"]!
  tap(safari.buttons["Show keyboard"]); sleep(2); keyboardVisible(); shot("iphone-keyboard-portrait")
  safari.typeText("sleep 60; printf BAD > \(p)/bad-interrupt\n")
  try await proof("bash","sleep")
  tap(safari.buttons["Ctrl+C"]); keyboardVisible()
  safari.typeText("printf interrupted > \(p)/interrupt\n"); try await proof("interrupt","interrupted")
  safari.typeText("cat \(p)/completion-uni"); tap(safari.buttons["Tab"])
  safari.typeText(" > \(p)/tab\n"); try await proof("tab","native-tab"); keyboardVisible()
  for (label,count) in [("escape",1),("arrows",12),("ctrl",2),("alt",3),("native",1)] {
   safari.typeText("python3 \(p)/read.py \(p) \(label) \(count)\n")
   try await proof(label+"-ready","ready")
   switch label {
    case "escape": tap(safari.buttons["Escape"])
    case "arrows": for key in ["Arrow up","Arrow down","Arrow left","Arrow right"] { tap(safari.buttons[key]) }
    case "ctrl": tap(safari.switches["Control"]); safari.typeText("a"); safari.typeText("b")
    case "alt": tap(safari.switches["Alt"]); safari.typeText("x"); safari.typeText("y")
    default: tap(safari.keyboards.keys["q"])
   }
   let expected=["escape":"1b","arrows":"1b5b411b5b421b5b441b5b43","ctrl":"0162","alt":"1b7879","native":"71"][label]!
   try await proof(label,expected); try await proof(label+"-done","done"); try await proof("bash","bash"); keyboardVisible()
  }
  safari.typeText("clear; printf 'Native Safari keyboard verified\\nCtrl+C, Tab, Esc, arrows and modifiers\\n'\n")
  shot("iphone-keyboard-verified")
  XCUIDevice.shared.orientation = .landscapeLeft; sleep(3); keyboardVisible(); shot("iphone-keyboard-landscape")
  XCUIDevice.shared.orientation = .portrait; sleep(2); keyboardVisible(); shot("iphone-keyboard-returned")
  print(safari.debugDescription)
 }
 func testNativeVideo() throws {
  continueAfterFailure=false; safari.activate(); XCUIDevice.shared.orientation = .portrait; sleep(2)
  tap(safari.buttons["Show keyboard"]); sleep(1)
  safari.typeText("clear; printf 'Deskyard shared terminal\\nNative iPhone keyboard\\n'\n"); sleep(1)
  print("QA_RECORD_START"); sleep(3)
  safari.typeText("sleep 60\n"); sleep(1); tap(safari.buttons["Ctrl+C"]); sleep(1)
  safari.typeText("printf 'Ctrl+C interrupted the foreground job\\n'\n"); sleep(1)
  shot("iphone-portrait-demo")
  XCUIDevice.shared.orientation = .landscapeLeft; sleep(3); shot("iphone-landscape-demo")
  XCUIDevice.shared.orientation = .portrait; sleep(3)
  print("QA_RECORD_STOP"); sleep(2)
 }
 func testLandscapeInspect() throws {
  safari.activate(); XCUIDevice.shared.orientation = .portrait; sleep(3); tap(safari.buttons["Show keyboard"]); sleep(2)
  if !safari.keyboards.firstMatch.exists { tap(safari.buttons["Show keyboard"]); sleep(2) }
  XCTAssertTrue(safari.keyboards.firstMatch.exists); shot("diagnostic-portrait")
  XCUIDevice.shared.orientation = .landscapeLeft; sleep(3); print(safari.debugDescription); shot("landscape-inspect")
  XCUIDevice.shared.orientation = .portrait; sleep(1)
 }
 func testDismissReopen() throws {
  continueAfterFailure=false; safari.activate(); XCUIDevice.shared.orientation = .portrait; sleep(2); tap(safari.buttons["Show keyboard"]); sleep(1); keyboardVisible()
  tap(safari.buttons["Done"]); sleep(1); XCTAssertFalse(safari.keyboards.firstMatch.exists); shot("iphone-keyboard-dismissed")
  tap(safari.buttons["Show keyboard"]); sleep(1); keyboardVisible(); shot("iphone-keyboard-reopened")
 } func testIPadAcceptance() async throws {
  continueAfterFailure=false; safari.activate(); XCUIDevice.shared.orientation = .portrait; sleep(3)
  let p=ProcessInfo.processInfo.environment["QA_PROOF_PATH"]!
  safari.buttons["Show keyboard"].tap(); sleep(2)
  if !safari.keyboards.firstMatch.exists { safari.buttons["Show keyboard"].tap(); sleep(2) }
  keyboardVisible(); shot("ipad-keyboard-before")
  safari.typeText("sleep 60; printf BAD > \(p)/bad-interrupt\n"); try await proof("bash","sleep")
  safari.buttons["Ctrl+C"].tap(); safari.typeText("printf interrupted > \(p)/interrupt\n"); try await proof("interrupt","interrupted")
  safari.typeText("cat \(p)/completion-uni"); safari.buttons["Tab"].tap(); safari.typeText(" > \(p)/tab\n"); try await proof("tab","native-tab")
  safari.typeText("python3 \(p)/read.py \(p) native 1\n"); try await proof("native-ready","ready")
  safari.keyboards.keys["q"].tap(); try await proof("native","71"); try await proof("native-done","done"); try await proof("bash","bash")
  safari.typeText("clear; printf 'Deskyard shared terminal\\nNative iPad keyboard verified\\nCtrl+C, Tab, and native q\\n'\n")
  keyboardVisible(); print(safari.debugDescription); shot("ipad-keyboard-verified")
 }


}
