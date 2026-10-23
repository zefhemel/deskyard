import XCTest
@MainActor
final class FinalTerminalQA: XCTestCase {
 let safari=XCUIApplication(bundleIdentifier:"com.apple.mobilesafari")
 let env=ProcessInfo.processInfo.environment
 func tap(_ e:XCUIElement) { XCTAssertTrue(e.waitForExistence(timeout:20)); safari.coordinate(withNormalizedOffset:.zero).withOffset(CGVector(dx:e.frame.midX,dy:e.frame.midY)).tap() }
 func shot(_ name:String,_ element:XCUIElement? = nil) { let a=XCTAttachment(screenshot:element?.screenshot() ?? XCUIScreen.main.screenshot());a.name=name;a.lifetime = .keepAlways;add(a) }
 func value(_ key:String) async throws -> String {
  let (data,response)=try await URLSession.shared.data(from:URL(string:env["QA_PROOF_URL"]!+"/"+key)!)
  return (response as? HTTPURLResponse)?.statusCode==200 ? String(decoding:data,as:UTF8.self) : ""
 }
 func proof(_ key:String,_ expected:String) async throws {
  for _ in 0..<60 { if try await value(key)==expected { return };try await Task.sleep(for:.milliseconds(250)) };XCTFail("Missing independent proof: "+key)
 }
 func focus() {
  tap(safari.buttons["Show keyboard"]);sleep(2)
  if !safari.keyboards.firstMatch.exists { tap(safari.buttons["Show keyboard"]);sleep(2) }
  XCTAssertTrue(safari.keyboards.firstMatch.exists)
 }
 func testFinalNative() async throws {
  continueAfterFailure=false;safari.activate();XCUIDevice.shared.orientation = .portrait;sleep(3)
  let p=env["QA_PROOF_PATH"]!, fresh=env["QA_NEW_SESSION"]!
  focus();shot("final-iphone-portrait-keyboard")
  safari.typeText("printf '%s' \"$$\" > \(p)/original-pid\n")
  safari.typeText("sleep 60; printf BAD > \(p)/bad-interrupt\n");try await proof("bash","sleep")
  tap(safari.buttons["Ctrl+C"]);safari.typeText("printf interrupted > \(p)/interrupt\n");try await proof("interrupt","interrupted")
  safari.typeText("cat \(p)/completion-uni");tap(safari.buttons["Tab"]);safari.typeText(" > \(p)/tab\n");try await proof("tab","native-tab")
  safari.typeText("python3 \(p)/read.py \(p) native 1\n");try await proof("native-ready","ready");tap(safari.keyboards.keys["q"]);try await proof("native","71");try await proof("native-done","done");try await proof("bash","bash")
  tap(safari.buttons["Done"]);sleep(1)
  tap(safari.buttons["Sessions: ios-qa"]);sleep(1);shot("final-iphone-picker-list")
  let field=safari.textFields["New session name"];tap(field);sleep(1);field.typeText(fresh);XCTAssertEqual(field.value as? String,fresh)
  tap(safari.buttons["Create session"]);XCTAssertTrue(safari.buttons["Sessions: "+fresh].waitForExistence(timeout:20));focus()
  safari.typeText("printf '%s' \"$$\" > \(p)/created-pid\n");sleep(1)
  tap(safari.buttons["Done"]);tap(safari.buttons["Sessions: "+fresh]);sleep(1);shot("final-iphone-picker-created")
  tap(safari.buttons["Join ios-qa"]);XCTAssertTrue(safari.buttons["Sessions: ios-qa"].waitForExistence(timeout:20));focus()
  safari.typeText("printf '%s' \"$$\" > \(p)/joined-pid\n");sleep(1)
  let original=try await value("original-pid"), created=try await value("created-pid"), joined=try await value("joined-pid")
  XCTAssertFalse(original.isEmpty);XCTAssertFalse(created.isEmpty);XCTAssertEqual(joined,original);XCTAssertNotEqual(created,original)
  safari.typeText("clear; printf 'Deskyard shared terminal\\nNative keyboard and session picker verified\\n'\n");shot("final-iphone-verified")
  XCUIDevice.shared.orientation = .landscapeLeft;sleep(3);XCTAssertTrue(safari.keyboards.firstMatch.exists)
  let fallback=safari.staticTexts["Rotate to portrait or dismiss the keyboard to use the terminal."]
  XCTAssertTrue(fallback.exists);shot("PRIVATE-final-landscape-full");shot("final-landscape-fallback",fallback)
  print("FALLBACK_FRAME \(fallback.frame) KEYBOARD_FRAME \(safari.keyboards.firstMatch.frame)")
  XCUIDevice.shared.orientation = .portrait;sleep(2);XCTAssertTrue(safari.keyboards.firstMatch.exists);XCTAssertFalse(fallback.isHittable);shot("final-iphone-returned")
  tap(safari.buttons["Done"]);sleep(1);XCTAssertFalse(safari.keyboards.firstMatch.exists);shot("final-iphone-dismissed");focus();shot("final-iphone-reopened")
 }
 func testFinalPortraitVideo() throws {
  continueAfterFailure=false;safari.activate();XCUIDevice.shared.orientation = .portrait;sleep(2);focus()
  safari.typeText("clear; printf 'Deskyard shared terminal\\nNative iPhone software keyboard\\n'\n");sleep(1)
  print("QA_RECORD_START");sleep(3);safari.typeText("sleep 60\n");sleep(1);tap(safari.buttons["Ctrl+C"])
  safari.typeText("printf 'Ctrl+C interrupted the job\\n'\n");sleep(3);tap(safari.buttons["Done"]);sleep(2);focus();sleep(3);print("QA_RECORD_STOP");sleep(2)
 }
}
