import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'onboarding_common.dart';
import 'vehicle_screen.dart';
import 'verification_screen.dart';

/// The hint under each document's name on D1.2.
const _hints = {
  ItemKind.profilePhoto: 'A clear selfie, no sunglasses',
  ItemKind.idProof: 'Aadhaar or passport',
  ItemKind.licence: 'Front and back, valid for commercial use',
  ItemKind.rc: 'Registration certificate',
  ItemKind.insurance: 'Commercial policy in force',
  ItemKind.permit: 'Taxi or contract-carriage permit',
  ItemKind.upi: 'Shown to riders so they can pay you',
};

/// D1.2 Your documents: seven rows (six photos and the UPI ID), each saved
/// as it's added. Rows: Upload / Add, Uploaded with Replace, Verified, Fix
/// with the admin's reason. Missing items are caught at submit.
class DocumentsScreen extends StatefulWidget {
  const DocumentsScreen({super.key});

  @override
  State<DocumentsScreen> createState() => _DocumentsScreenState();
}

class _DocumentsScreenState extends State<DocumentsScreen> {
  Driver? _driver;
  Object? _error;
  final Set<String> _uploading = {};

  /// Kind → why the last upload failed.
  final Map<String, String?> _failed = {};

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_driver == null && _error == null) _load();
  }

  Future<void> _load() async {
    try {
      final d = await AppScope.of(context).api.me();
      if (mounted) {
        setState(() {
          _driver = d;
          _error = null;
        });
      }
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  void _retry() {
    setState(() => _error = null);
    _load();
  }

  Future<void> _tapRow(DriverItem item) async {
    if (item.kind == ItemKind.upi) {
      final saved = await showModalBottomSheet<bool>(
        context: context,
        isScrollControlled: true,
        backgroundColor: AppColors.card,
        showDragHandle: true,
        builder: (_) => _UpiSheet(initial: _driver?.upiId ?? item.value ?? ''),
      );
      if (saved == true) await _load();
      return;
    }
    final photo = await choosePhoto(context);
    if (photo == null || !mounted) return;
    setState(() {
      _uploading.add(item.kind);
      _failed.remove(item.kind);
    });
    try {
      await AppScope.of(context).api.uploadDocument(item.kind, photo.bytes, photo.contentType);
      await _load();
    } on ApiException catch (e) {
      if (!mounted) return;
      const explained = {'file_too_large', 'bad_content_type', 'bad_file_type', 'locked'};
      setState(() => _failed[item.kind] = explained.contains(e.code) ? e.message : null);
    } finally {
      if (mounted) setState(() => _uploading.remove(item.kind));
    }
  }

  void _continue() {
    final d = _driver;
    final nav = Navigator.of(context);
    if (d == null || d.status == DriverStatus.signingUp) {
      nav.push(MaterialPageRoute<void>(builder: (_) => const VehicleScreen()));
    } else if (nav.canPop()) {
      nav.pop();
    } else {
      nav.pushReplacement(MaterialPageRoute<void>(builder: (_) => const VerificationScreen()));
    }
  }

  @override
  Widget build(BuildContext context) {
    final d = _driver;
    final signingUp = d == null || d.status == DriverStatus.signingUp;
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(label: 'Step 2 of 5'),
            Expanded(
              child: d == null
                  ? LoadingBody(error: _error, onRetry: _retry)
                  : RefreshIndicator(
                      color: AppColors.yellow,
                      onRefresh: _load,
                      child: ListView(
                        key: const Key('documents.list'),
                        physics: const AlwaysScrollableScrollPhysics(),
                        padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
                        children: [
                          Semantics(header: true, child: Text('Your documents', style: AppText.display(28))),
                          const SizedBox(height: 10),
                          Row(
                            children: [
                              Text('${d.documentsAdded} of 7 added', style: AppText.body(14, weight: FontWeight.w600)),
                              const Spacer(),
                              Text('Takes about 10 minutes', style: AppText.body(13, color: AppColors.muted)),
                            ],
                          ),
                          const SizedBox(height: 8),
                          ClipRRect(
                            borderRadius: BorderRadius.circular(4),
                            child: LinearProgressIndicator(
                              key: const Key('documents.progress'),
                              value: d.documentsAdded / 7,
                              minHeight: 6,
                              color: AppColors.yellow,
                              backgroundColor: AppColors.cardRaised,
                            ),
                          ),
                          const SizedBox(height: 16),
                          for (final item in d.documents) ...[_row(d, item), const SizedBox(height: 10)],
                          const SizedBox(height: 6),
                          const NoteBox(
                            icon: Icons.lock_outline,
                            text: 'Take clear photos of the originals. Documents are only used to verify you '
                                'and are never shown to riders.',
                          ),
                        ],
                      ),
                    ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
              child: PrimaryButton(
                label: signingUp ? 'Continue to vehicle' : 'Done',
                height: 52,
                onPressed: d == null ? null : _continue,
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _row(Driver d, DriverItem item) {
    final kind = item.kind;
    final editable = d.canEdit(kind);
    final uploading = _uploading.contains(kind);
    final failed = _failed.containsKey(kind);
    final isUpi = kind == ItemKind.upi;
    final Widget trailing;
    if (uploading) {
      trailing = const SizedBox(
        width: 22,
        height: 22,
        child: CircularProgressIndicator(strokeWidth: 2.4, color: AppColors.yellow),
      );
    } else {
      trailing = switch (item.state) {
        ItemState.missing => SmallButton(label: isUpi ? 'Add' : 'Upload', onPressed: editable ? () => _tapRow(item) : null),
        ItemState.uploaded => Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              const ItemStateLabel('Uploaded'),
              if (editable) LinkButton(label: 'Replace', height: 32, size: 13, onPressed: () => _tapRow(item)),
            ],
          ),
        ItemState.verified => const ItemStateLabel('Verified', color: AppColors.green, icon: Icons.check_circle),
        ItemState.rejected => SmallButton(label: 'Fix', color: AppColors.orange, onPressed: editable ? () => _tapRow(item) : null),
      };
    }
    final value = isUpi ? (d.upiId ?? item.value) : null;
    return Material(
      key: Key('doc.$kind'),
      color: AppColors.card,
      borderRadius: BorderRadius.circular(16),
      child: InkWell(
        borderRadius: BorderRadius.circular(16),
        onTap: editable && !uploading && item.state != ItemState.verified ? () => _tapRow(item) : null,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 12, 12, 12),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(ItemKind.label(kind), style: AppText.body(15, weight: FontWeight.w600)),
                    const SizedBox(height: 2),
                    Text(_hints[kind] ?? '', style: AppText.body(13, color: AppColors.muted, height: 1.35)),
                    if (value != null && item.state != ItemState.missing) ...[
                      const SizedBox(height: 2),
                      Text(value, style: AppText.body(13, weight: FontWeight.w500)),
                    ],
                    if (item.state == ItemState.rejected && item.rejectReason != null && !uploading) ...[
                      const SizedBox(height: 4),
                      Text(item.rejectReason!, style: AppText.body(13, color: AppColors.orange, height: 1.35)),
                    ],
                    if (uploading) ...[
                      const SizedBox(height: 6),
                      Text('Uploading…', style: AppText.body(12, color: AppColors.muted)),
                      const SizedBox(height: 4),
                      const LinearProgressIndicator(minHeight: 3, color: AppColors.yellow, backgroundColor: AppColors.cardRaised),
                    ],
                    if (failed && !uploading) ...[
                      const SizedBox(height: 4),
                      Text('Upload failed. Try again.', style: AppText.body(13, color: AppColors.orange)),
                      if (_failed[kind] != null) Text(_failed[kind]!, style: AppText.body(12, color: AppColors.muted)),
                    ],
                  ],
                ),
              ),
              const SizedBox(width: 10),
              trailing,
            ],
          ),
        ),
      ),
    );
  }
}

/// Add or change the UPI ID (driver.set_upi).
class _UpiSheet extends StatefulWidget {
  const _UpiSheet({required this.initial});

  final String initial;

  @override
  State<_UpiSheet> createState() => _UpiSheetState();
}

class _UpiSheetState extends State<_UpiSheet> {
  late final _upi = TextEditingController(text: widget.initial);
  bool _saving = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _upi.addListener(() {
      if (_error != null) setState(() => _error = null);
    });
  }

  @override
  void dispose() {
    _upi.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final v = _upi.text.trim();
    if (v.isEmpty) {
      setState(() => _error = 'Enter a UPI ID like name@bank.');
      return;
    }
    setState(() => _saving = true);
    try {
      await AppScope.of(context).api.setUpi(v);
      if (mounted) Navigator.of(context).pop(true);
    } on ApiException catch (e) {
      if (mounted) {
        setState(() {
          _saving = false;
          _error = e.message;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.viewInsetsOf(context).bottom),
      child: SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 0, 20, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Your UPI ID', style: AppText.display(22)),
              const SizedBox(height: 6),
              Text('Shown to riders so they can pay you.', style: AppText.body(14, color: AppColors.muted)),
              const SizedBox(height: 14),
              LabelledField(
                label: 'UPI ID',
                controller: _upi,
                hint: 'name@bank',
                keyboardType: TextInputType.emailAddress,
                error: _error,
                onSubmitted: (_) => _saving ? null : _save(),
              ),
              const SizedBox(height: 14),
              PrimaryButton(label: 'Save', height: 52, loading: _saving, onPressed: _save),
            ],
          ),
        ),
      ),
    );
  }
}
