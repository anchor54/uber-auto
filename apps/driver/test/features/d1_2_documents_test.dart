import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// D1.2 Your documents: the seven rows and each row state (Upload / Add,
/// Uploaded + Replace, Verified, Fix with the reason), upload through the
/// camera-or-gallery chooser, and a failed upload.
void main() {
  Future<D1Robot> open(WidgetTester tester, {Future<void> Function(FakeDriverApi api)? setup}) async {
    final h = await pumpD1Screen(tester, () => DocumentsScreen(), setup: setup ?? d1SignedUp);
    return D1Robot(tester, h);
  }

  Future<ItemState> stateOf(D1Robot robot, String kind) async =>
      (await robot.call(() => robot.api.me())).items.singleWhere((i) => i.kind == kind).state;

  group('D1.2 Documents', () {
    testWidgets('D1.2 header, seven rows with names and hints, note, button', (tester) async {
      final robot = await open(tester);
      expect(find.textContaining('Step 2 of 5'), findsOneWidget);
      expect(find.text('Your documents'), findsWidgets);
      expect(find.text('0 of 7 added'), findsOneWidget);
      expect(find.text('Takes about 10 minutes'), findsOneWidget);
      for (final kind in documentKinds) {
        await robot.scrollTo(robot.row(kind));
        expect(robot.inRow(kind, find.text(documentNames[kind]!)), findsOneWidget, reason: kind);
        expect(robot.inRow(kind, find.text(documentHints[kind]!)), findsOneWidget, reason: kind);
        expect(robot.inRow(kind, find.text(kind == 'upi' ? 'Add' : 'Upload')), findsOneWidget, reason: kind);
      }
      await robot.scrollTo(find.textContaining('Take clear photos of the originals.'));
      expect(
        find.text('Take clear photos of the originals. Documents are only used to verify you and are never shown to riders.'),
        findsOneWidget,
      );
      expect(robot.label('Continue to vehicle'), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.2 Upload offers camera or gallery, shows a preview, then "Uploaded" with Replace', (tester) async {
      final robot = await open(tester);
      await robot.startUpload('licence');
      expect(find.textContaining(RegExp('camera', caseSensitive: false)), findsWidgets);
      expect(find.textContaining(RegExp('gallery', caseSensitive: false)), findsWidgets);
      final camera = await robot.firstOf([robot.key('camera'), robot.key('take_photo'), find.textContaining(RegExp('camera', caseSensitive: false))]);
      await robot.tapFinder(camera.last);
      expect(robot.h.images.asked, [ImageOrigin.camera]);
      expect(find.text('Use this photo'), findsOneWidget);
      expect(find.byType(Image), findsWidgets, reason: 'the preview');
      await robot.tapFinder(await robot.firstOf([robot.key('use_this_photo'), find.text('Use this photo')]));

      await robot.scrollTo(robot.row('licence'));
      expect(robot.inRow('licence', find.text('Uploaded')), findsOneWidget);
      expect(robot.inRow('licence', find.text('Replace')), findsOneWidget);
      expect(robot.inRow('licence', find.text('Upload')), findsNothing);
      expect(find.text('1 of 7 added'), findsOneWidget);
      expect(await stateOf(robot, 'licence'), ItemState.uploaded);
      await leaveD1(tester);
    });

    testWidgets('D1.2 from the gallery works too', (tester) async {
      final robot = await open(tester);
      await robot.startUpload('rc');
      final gallery = await robot.firstOf([robot.key('gallery'), robot.key('choose_from_gallery'), find.textContaining(RegExp('gallery', caseSensitive: false))]);
      await robot.tapFinder(gallery.last);
      expect(robot.h.images.asked, [ImageOrigin.gallery]);
      await robot.tapFinder(await robot.firstOf([robot.key('use_this_photo'), find.text('Use this photo')]));
      expect(await stateOf(robot, 'rc'), ItemState.uploaded);
      await leaveD1(tester);
    });

    testWidgets('D1.2 cancelling the picker changes nothing', (tester) async {
      final robot = await open(tester);
      robot.h.images.cancel = true;
      await robot.startUpload('licence');
      final camera = await robot.firstOf([robot.key('camera'), robot.key('take_photo'), find.textContaining(RegExp('camera', caseSensitive: false))]);
      await robot.tapFinder(camera.last);
      expect(find.text('Use this photo'), findsNothing);
      await robot.scrollTo(robot.row('licence'));
      expect(robot.inRow('licence', find.text('Upload')), findsOneWidget);
      expect(find.text('0 of 7 added'), findsOneWidget);
      expect(await stateOf(robot, 'licence'), ItemState.missing);
      await leaveD1(tester);
    });

    testWidgets('D1.2 a failed upload: "Upload failed. Try again." in the row; trying again works', (tester) async {
      final robot = await open(tester);
      robot.api.failNextUploads = 1;
      await robot.uploadDoc('insurance');
      await robot.scrollTo(robot.row('insurance'));
      expect(find.text('Upload failed. Try again.'), findsOneWidget);
      expect(robot.inRow('insurance', find.text('Uploaded')), findsNothing);
      expect(await stateOf(robot, 'insurance'), ItemState.missing);
      expect(find.text('0 of 7 added'), findsOneWidget);

      await robot.uploadDoc('insurance');
      await robot.scrollTo(robot.row('insurance'));
      expect(robot.inRow('insurance', find.text('Uploaded')), findsOneWidget);
      expect(find.text('Upload failed. Try again.'), findsNothing);
      expect(await stateOf(robot, 'insurance'), ItemState.uploaded);
      await leaveD1(tester);
    });

    testWidgets('D1.2 a photo over 5 MB is refused and nothing is saved', (tester) async {
      final robot = await open(tester);
      robot.h.images.next = oversizeJpeg();
      await robot.uploadDoc('permit');
      await robot.scrollTo(robot.row('permit'));
      expect(robot.inRow('permit', find.text('Uploaded')), findsNothing);
      expect(find.textContaining(RegExp(r'Upload failed\. Try again\.|5 MB')), findsWidgets);
      expect(await stateOf(robot, 'permit'), ItemState.missing);
      await leaveD1(tester);
    });

    testWidgets('D1.2 UPI ID: Add, then saved and counted', (tester) async {
      final robot = await open(tester);
      await robot.addUpi(signUpUpi);
      final d = await robot.call(() => robot.api.me());
      expect(d.upiId, signUpUpi);
      await robot.scrollTo(find.textContaining('of 7 added'));
      expect(find.text('1 of 7 added'), findsOneWidget);
      await robot.scrollTo(robot.row('upi'));
      expect(robot.inRow('upi', find.text('Add')), findsNothing);
      await leaveD1(tester);
    });

    testWidgets('D1.2 all seven: "7 of 7 added"', (tester) async {
      final robot = await open(tester);
      await robot.addAllDocuments();
      await robot.scrollTo(find.textContaining('of 7 added'));
      expect(find.text('7 of 7 added'), findsOneWidget);
      expect((await robot.call(() => robot.api.me())).documentsAdded, 7);
      await leaveD1(tester);
    });

    testWidgets('D1.2 an item uploaded earlier shows "Uploaded" when the screen opens', (tester) async {
      final robot = await open(tester, setup: (api) async {
        await d1SignedUp(api);
        await d1AddDocuments(api, kinds: ['profile_photo', 'id_proof'], upi: false);
      });
      expect(find.text('2 of 7 added'), findsOneWidget);
      expect(robot.inRow('profile_photo', find.text('Uploaded')), findsOneWidget);
      expect(robot.inRow('id_proof', find.text('Uploaded')), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.2 "Continue to vehicle" is enabled with items missing', (tester) async {
      final robot = await open(tester);
      await robot.tap('continue_to_vehicle');
      expect(find.text('Your vehicle'), findsWidgets);
      await leaveD1(tester);
    });

    testWidgets('D1.2 changes needed: "Fix" with the reason; verified rows locked; the rejected one can be replaced',
        (tester) async {
      final robot = await open(tester, setup: (api) async {
        await d1Submitted(api);
        api.adminRequestChanges('licence', 'photo blurred');
      });
      await robot.scrollTo(robot.row('licence'));
      expect(robot.inRow('licence', find.text('Fix')), findsOneWidget);
      expect(robot.inRow('licence', find.text('photo blurred')), findsOneWidget);
      for (final kind in documentKinds.where((k) => k != 'licence')) {
        await robot.scrollTo(robot.row(kind));
        expect(robot.inRow(kind, find.text('Verified')), findsOneWidget, reason: kind);
        expect(robot.inRow(kind, find.text('Replace')), findsNothing, reason: kind);
        expect(robot.inRow(kind, find.text('Upload')), findsNothing, reason: kind);
      }

      await robot.uploadDoc('licence');
      await robot.scrollTo(robot.row('licence'));
      expect(robot.inRow('licence', find.text('Uploaded')), findsOneWidget);
      expect(robot.inRow('licence', find.text('photo blurred')), findsNothing);
      expect(await stateOf(robot, 'licence'), ItemState.uploaded);
      expect(await stateOf(robot, 'rc'), ItemState.verified);
      await leaveD1(tester);
    });

    testWidgets('D1.2 after submit: read only, no Replace', (tester) async {
      final robot = await open(tester, setup: d1Submitted);
      expect(find.text('Replace'), findsNothing);
      expect(find.text('Upload'), findsNothing);
      await robot.scrollTo(robot.row('licence'));
      expect(robot.inRow('licence', find.text('Uploaded')), findsOneWidget);
      await leaveD1(tester);
    });
  });
}
