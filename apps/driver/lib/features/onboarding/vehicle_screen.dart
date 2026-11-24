import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'day_off_screen.dart';
import 'onboarding_common.dart';

/// D1.3 Your vehicle: type (and cab class), make and model, year, plate,
/// colour, AC and CNG switches, and three photos (front, back, interior).
/// Photos go up as they're taken; the details are saved on Continue.
class VehicleScreen extends StatefulWidget {
  const VehicleScreen({super.key});

  @override
  State<VehicleScreen> createState() => _VehicleScreenState();
}

const _types = [('bike', 'Bike'), ('auto', 'Auto'), ('cab', 'Cab')];
const _classes = [('hatchback', 'Hatchback'), ('sedan', 'Sedan'), ('suv', 'SUV')];
const _photoTiles = [(ItemKind.vehicleFront, 'Front'), (ItemKind.vehicleBack, 'Back'), (ItemKind.vehicleInterior, 'Interior')];

class _VehicleScreenState extends State<VehicleScreen> {
  Driver? _driver;
  Object? _loadError;
  bool _prefilled = false;

  String? _type;
  String? _cabClass;
  final _makeModel = TextEditingController();
  final _year = TextEditingController();
  final _plate = TextEditingController();
  final _colour = TextEditingController();
  bool _ac = false;
  bool _cng = false;

  bool _saving = false;
  String? _error;
  String? _plateError;
  final Set<String> _uploading = {};
  final Map<String, String?> _failed = {};

  @override
  void initState() {
    super.initState();
    for (final c in [_makeModel, _year, _plate, _colour]) {
      c.addListener(_edited);
    }
  }

  void _edited() {
    if (!mounted) return;
    setState(() {
      _error = null;
      _plateError = null;
    });
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_driver == null && _loadError == null) _load();
  }

  @override
  void dispose() {
    for (final c in [_makeModel, _year, _plate, _colour]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final d = await AppScope.of(context).api.me();
      if (!mounted) return;
      setState(() {
        _driver = d;
        _loadError = null;
        if (!_prefilled) _prefill(d.vehicle);
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _loadError = e);
    }
  }

  void _prefill(VehicleDetails? v) {
    _prefilled = true;
    if (v == null) return;
    _type = v.type;
    _cabClass = v.cabClass;
    _makeModel.text = v.makeModel;
    _year.text = v.year?.toString() ?? '';
    _plate.text = v.plate;
    _colour.text = v.colour;
    _ac = v.ac;
    _cng = v.cngOrElectric;
  }

  bool get _detailsEditable => _driver?.canEdit(ItemKind.vehicle) ?? false;

  bool get _valid =>
      _type != null &&
      (_type != 'cab' || _cabClass != null) &&
      _makeModel.text.trim().isNotEmpty &&
      RegExp(r'^\d{4}$').hasMatch(_year.text.trim()) &&
      _plate.text.trim().length >= 4 &&
      _colour.text.trim().isNotEmpty;

  /// Whether the form differs from what the backend has (or must be sent again).
  bool _changed(VehicleDetails? v) {
    if (v == null || v.reviewState == ItemState.rejected) return true;
    return v.type != _type ||
        (v.cabClass ?? '') != (_type == 'cab' ? (_cabClass ?? '') : '') ||
        v.makeModel != _makeModel.text.trim() ||
        v.year?.toString() != _year.text.trim() ||
        v.plate.toUpperCase().replaceAll(' ', '') != _plate.text.trim().toUpperCase().replaceAll(' ', '') ||
        v.colour != _colour.text.trim() ||
        v.ac != (_type == 'cab' && _ac) ||
        v.cngOrElectric != _cng;
  }

  Future<void> _continue() async {
    final d = _driver;
    if (d == null) return;
    if (_detailsEditable && _changed(d.vehicle)) {
      setState(() => _saving = true);
      try {
        await AppScope.of(context).api.setVehicle(
              type: _type!,
              cabClass: _type == 'cab' ? _cabClass : null,
              makeModel: _makeModel.text.trim(),
              year: int.parse(_year.text.trim()),
              plate: _plate.text.trim(),
              colour: _colour.text.trim(),
              ac: _type == 'cab' && _ac,
              cngOrElectric: _cng,
            );
      } on ApiException catch (e) {
        if (!mounted) return;
        setState(() {
          _saving = false;
          if (e.code == 'plate_in_use' || e.code == 'bad_plate') {
            _plateError = e.code == 'plate_in_use' ? '${e.message} $supportLine' : e.message;
          } else {
            _error = e.message;
          }
        });
        return;
      }
      if (!mounted) return;
      setState(() => _saving = false);
      await _load();
      if (!mounted) return;
    }
    final nav = Navigator.of(context);
    if (d.status == DriverStatus.signingUp) {
      await nav.push(MaterialPageRoute<void>(builder: (_) => const DayOffScreen()));
      if (mounted) _load();
    } else {
      nav.maybePop();
    }
  }

  Future<void> _photo(String kind) async {
    final photo = await choosePhoto(context);
    if (photo == null || !mounted) return;
    setState(() {
      _uploading.add(kind);
      _failed.remove(kind);
    });
    try {
      await AppScope.of(context).api.uploadDocument(kind, photo.bytes, photo.contentType);
      await _load();
    } on ApiException catch (e) {
      if (!mounted) return;
      const explained = {'file_too_large', 'bad_content_type', 'bad_file_type', 'locked'};
      setState(() => _failed[kind] = explained.contains(e.code) ? e.message : null);
    } finally {
      if (mounted) setState(() => _uploading.remove(kind));
    }
  }

  @override
  Widget build(BuildContext context) {
    final d = _driver;
    final editable = _detailsEditable;
    final canContinue = d != null && !_saving && (!editable || !_changed(d.vehicle) || _valid);
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(label: 'Step 3 of 5'),
            Expanded(
              child: d == null
                  ? LoadingBody(
                      error: _loadError,
                      onRetry: () {
                        setState(() => _loadError = null);
                        _load();
                      },
                    )
                  : ListView(
                      key: const Key('vehicle.list'),
                      padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
                      children: [
                        Semantics(header: true, child: Text('Your vehicle', style: AppText.display(28))),
                        if (d.vehicle?.reviewState == ItemState.rejected && d.vehicle?.rejectReason != null) ...[
                          const SizedBox(height: 10),
                          NoteBox(icon: Icons.error_outline, color: AppColors.orange, text: 'Fix: ${d.vehicle!.rejectReason!}'),
                        ],
                        const SizedBox(height: 16),
                        _label('Vehicle type'),
                        Wrap(
                          spacing: 8,
                          children: [
                            for (final (value, name) in _types)
                              ChoiceChip(
                                key: labelKey(name),
                                label: Text(name),
                                selected: _type == value,
                                onSelected: editable ? (_) => setState(() => _type = value) : null,
                              ),
                          ],
                        ),
                        if (_type == 'cab') ...[
                          const SizedBox(height: 8),
                          Wrap(
                            spacing: 8,
                            children: [
                              for (final (value, name) in _classes)
                                ChoiceChip(
                                  key: labelKey(name),
                                  label: Text(name),
                                  selected: _cabClass == value,
                                  onSelected: editable ? (_) => setState(() => _cabClass = value) : null,
                                ),
                            ],
                          ),
                        ],
                        const SizedBox(height: 16),
                        LabelledField(label: 'Make and model', controller: _makeModel, hint: 'e.g. Bajaj RE', enabled: editable),
                        const SizedBox(height: 14),
                        LabelledField(
                          label: 'Year',
                          controller: _year,
                          hint: 'e.g. 2021',
                          enabled: editable,
                          keyboardType: TextInputType.number,
                          inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(4)],
                        ),
                        const SizedBox(height: 14),
                        LabelledField(
                          label: 'Number plate',
                          controller: _plate,
                          hint: 'e.g. KA 05 AB 1234',
                          enabled: editable,
                          textCapitalization: TextCapitalization.characters,
                          error: _plateError,
                        ),
                        const SizedBox(height: 14),
                        LabelledField(label: 'Colour', controller: _colour, hint: 'e.g. Green', enabled: editable),
                        const SizedBox(height: 8),
                        if (_type == 'cab') _switch('Working AC', _ac, editable ? (v) => setState(() => _ac = v) : null),
                        _switch('CNG or electric', _cng, editable ? (v) => setState(() => _cng = v) : null),
                        const SizedBox(height: 12),
                        _label('Vehicle photos'),
                        Row(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            for (final (i, (kind, name)) in _photoTiles.indexed) ...[
                              if (i > 0) const SizedBox(width: 10),
                              Expanded(child: _tile(d, kind, name)),
                            ],
                          ],
                        ),
                        for (final (kind, name) in _photoTiles)
                          if (_failed.containsKey(kind)) ...[
                            const SizedBox(height: 6),
                            Text('$name: Upload failed. Try again.', style: AppText.body(13, color: AppColors.orange)),
                            if (_failed[kind] != null) Text(_failed[kind]!, style: AppText.body(12, color: AppColors.muted)),
                          ],
                        for (final (kind, name) in _photoTiles)
                          if (d.item(kind).state == ItemState.rejected && d.item(kind).rejectReason != null) ...[
                            const SizedBox(height: 6),
                            Text('$name: ${d.item(kind).rejectReason!}', style: AppText.body(13, color: AppColors.orange)),
                          ],
                        const SizedBox(height: 16),
                        const NoteBox(text: "Riders see the type, model, colour and plate when you're matched."),
                        if (_error != null) ...[
                          const SizedBox(height: 10),
                          Text(_error!, key: const Key('vehicle.error'), style: AppText.body(14, color: AppColors.orange, height: 1.4)),
                        ],
                      ],
                    ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
              child: PrimaryButton(label: 'Continue', height: 52, loading: _saving, onPressed: canContinue ? _continue : null),
            ),
          ],
        ),
      ),
    );
  }

  Widget _label(String text) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Text(text, style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted)),
      );

  Widget _switch(String label, bool value, ValueChanged<bool>? onChanged) => SizedBox(
        height: 48,
        child: Row(
          children: [
            Expanded(child: Text(label, style: AppText.body(15))),
            Switch(
              key: labelKey(label),
              value: value,
              activeTrackColor: AppColors.yellow,
              onChanged: onChanged,
            ),
          ],
        ),
      );

  Widget _tile(Driver d, String kind, String name) {
    final item = d.item(kind);
    final editable = d.canEdit(kind) && item.state != ItemState.verified;
    final uploading = _uploading.contains(kind);
    final (String state, Color color, IconData icon) = switch (item.state) {
      ItemState.missing => ('Add photo', AppColors.yellow, Icons.add_a_photo_outlined),
      ItemState.uploaded => ('Uploaded', AppColors.muted, Icons.check),
      ItemState.verified => ('Verified', AppColors.green, Icons.check_circle),
      ItemState.rejected => ('Fix', AppColors.orange, Icons.error_outline),
    };
    return Semantics(
      button: editable,
      label: '$name photo, $state',
      excludeSemantics: true,
      child: Material(
        key: Key('doc.$kind'),
        color: AppColors.card,
        borderRadius: BorderRadius.circular(14),
        child: InkWell(
          borderRadius: BorderRadius.circular(14),
          onTap: editable && !uploading ? () => _photo(kind) : null,
          child: Container(
            height: 96,
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(14),
              border: Border.all(color: item.state == ItemState.rejected ? AppColors.orange : AppColors.line),
            ),
            padding: const EdgeInsets.all(8),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                if (uploading)
                  const SizedBox(width: 22, height: 22, child: CircularProgressIndicator(strokeWidth: 2.4, color: AppColors.yellow))
                else
                  Icon(icon, color: color, size: 22),
                const SizedBox(height: 6),
                Text(name, style: AppText.body(14, weight: FontWeight.w600)),
                Text(uploading ? 'Uploading…' : state, style: AppText.body(12, color: color)),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
