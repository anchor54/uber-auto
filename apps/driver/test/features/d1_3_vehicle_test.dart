import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// D1.3 Your vehicle: type (and cab class), details, switches, the three
/// photo tiles; Continue saves and opens D1.4.
void main() {
  Future<D1Robot> open(WidgetTester tester, {Future<void> Function(FakeDriverApi api)? setup}) async {
    final h = await pumpD1Screen(tester, () => VehicleScreen(), setup: setup ?? d1SignedUp);
    return D1Robot(tester, h);
  }

  group('D1.3 Vehicle', () {
    testWidgets('D1.3 copy: step, title, types, fields, switches, tiles, note', (tester) async {
      final robot = await open(tester);
      expect(find.textContaining('Step 3 of 5'), findsOneWidget);
      expect(find.text('Your vehicle'), findsWidgets);
      for (final t in ['Bike', 'Auto', 'Cab']) {
        expect(find.text(t), findsOneWidget, reason: t);
      }
      expect(find.text('Sedan'), findsNothing, reason: 'the class row shows only for cabs');
      for (final f in ['Make and model', 'Year', 'Number plate', 'Colour']) {
        await robot.scrollTo(find.text(f));
        expect(find.text(f), findsWidgets, reason: f);
      }
      await robot.scrollTo(find.text('CNG or electric'));
      expect(find.text('CNG or electric'), findsOneWidget);
      for (final t in ['Front', 'Back', 'Interior']) {
        await robot.scrollTo(find.text(t));
        expect(find.text(t), findsOneWidget, reason: t);
      }
      await robot.scrollTo(find.text("Riders see the type, model, colour and plate when you're matched."));
      expect(find.text("Riders see the type, model, colour and plate when you're matched."), findsOneWidget);
      expect(robot.label('Continue'), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.3 Cab adds Hatchback / Sedan / SUV and the Working AC switch', (tester) async {
      final robot = await open(tester);
      await robot.tapFinder(find.text('Auto'));
      expect(find.text('Hatchback'), findsNothing);
      expect(find.textContaining('Working AC'), findsNothing, reason: 'cabs only');
      await robot.tapFinder(find.text('Cab'));
      for (final c in ['Hatchback', 'Sedan', 'SUV']) {
        expect(find.text(c), findsOneWidget, reason: c);
      }
      await robot.scrollTo(find.textContaining('Working AC'));
      expect(find.textContaining('Working AC'), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.3 an auto with three photos: saved, plate upper-cased, then D1.4', (tester) async {
      final robot = await open(tester);
      await robot.fillVehicle();
      expect(robot.h.images.asked, hasLength(3));
      await robot.tap('continue');
      expect(find.text('Your day off'), findsWidgets);
      final d = await robot.call(() => robot.api.me());
      final v = d.vehicle!;
      expect(v.type, 'auto');
      expect(v.cabClass, isNull);
      expect(v.makeModel, 'Bajaj RE');
      expect(v.year, 2021);
      expect(v.plate, 'KA 05 CD 5678');
      expect(v.colour, 'Green');
      for (final k in vehiclePhotoKinds) {
        expect(d.items.singleWhere((i) => i.kind == k).state, ItemState.uploaded, reason: k);
      }
      await leaveD1(tester);
    });

    testWidgets('D1.3 a sedan cab', (tester) async {
      final robot = await open(tester);
      await robot.fillVehicle(type: 'Cab', cabClass: 'Sedan', makeModel: 'Maruti Dzire', year: '2022', plate: 'KA01MN4321', colour: 'White', photos: false);
      await robot.tap('continue');
      final v = (await robot.call(() => robot.api.me())).vehicle!;
      expect(v.type, 'cab');
      expect(v.cabClass, 'sedan');
      expect(v.plate, 'KA01MN4321');
      await leaveD1(tester);
    });

    testWidgets('D1.3 a photo tile takes, then replaces, its photo', (tester) async {
      final robot = await open(tester);
      await robot.tapFinder(await robot.firstOf([robot.row('vehicle_front'), find.text('Front')]));
      await robot.choosePhoto();
      var d = await robot.call(() => robot.api.me());
      expect(d.items.singleWhere((i) => i.kind == 'vehicle_front').state, ItemState.uploaded);
      expect(d.items.singleWhere((i) => i.kind == 'vehicle_back').state, ItemState.missing);
      await robot.tapFinder(await robot.firstOf([robot.row('vehicle_front'), find.text('Front')]));
      await robot.choosePhoto();
      d = await robot.call(() => robot.api.me());
      expect(d.items.singleWhere((i) => i.kind == 'vehicle_front').state, ItemState.uploaded);
      expect(robot.h.images.asked, hasLength(2));
      await leaveD1(tester);
    });

    testWidgets('D1.3 opens with the saved vehicle', (tester) async {
      final robot = await open(tester, setup: (api) async {
        await d1SignedUp(api);
        await d1AddVehicle(api);
      });
      await robot.scrollTo(find.text('KA 05 CD 5678'));
      expect(find.text('KA 05 CD 5678'), findsOneWidget);
      expect(find.text('Bajaj RE'), findsOneWidget);
      await leaveD1(tester);
    });
  });
}
