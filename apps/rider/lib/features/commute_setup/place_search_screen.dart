import 'dart:async';

import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/icons.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../auth/welcome_screen.dart';

/// Search for a pickup or drop-off. Returns the chosen [Place].
class PlaceSearchScreen extends StatefulWidget {
  const PlaceSearchScreen({super.key, required this.title, this.initialQuery = ''});

  final String title;
  final String initialQuery;

  @override
  State<PlaceSearchScreen> createState() => _PlaceSearchScreenState();
}

class _PlaceSearchScreenState extends State<PlaceSearchScreen> {
  late final _query = TextEditingController(text: widget.initialQuery);
  List<Place> _results = const [];
  bool _loaded = false;
  int _requestId = 0;
  String? _error;
  Timer? _debounce;

  /// Real mode asks a paid maps API, so it waits for a pause in typing.
  static const debounce = Duration(milliseconds: 300);

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_loaded) {
      _loaded = true;
      _search(_query.text);
    }
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _query.dispose();
    super.dispose();
  }

  void _onChanged(String q) {
    _debounce?.cancel();
    if (AppScope.of(context).demoMode) {
      _search(q);
    } else {
      _debounce = Timer(debounce, () => _search(q));
    }
  }

  Future<void> _search(String q) async {
    final id = ++_requestId;
    try {
      final results = await AppScope.of(context).api.searchPlaces(q);
      if (!mounted || id != _requestId) return;
      setState(() {
        _results = results;
        _error = null;
      });
    } on ApiException catch (e) {
      if (!mounted || id != _requestId) return;
      setState(() {
        _results = const [];
        _error = e.message;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            FlowHeader(label: widget.title),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
              child: TextField(
                key: const Key('place.query'),
                controller: _query,
                autofocus: true,
                textInputAction: TextInputAction.search,
                onChanged: _onChanged,
                style: AppText.body(15, weight: FontWeight.w500),
                decoration: riderInputDecoration('Search a place or address').copyWith(
                  prefixIcon: const Padding(
                    padding: EdgeInsets.only(left: 12, right: 8),
                    child: AppIcon(AppIcons.search, size: 20, color: AppColors.muted),
                  ),
                  prefixIconConstraints: const BoxConstraints(minWidth: 40, minHeight: 20),
                ),
              ),
            ),
            Expanded(
              child: _results.isEmpty
                  ? Padding(
                      padding: const EdgeInsets.all(24),
                      child: Text(
                        _error ?? (_query.text.isEmpty ? 'Start typing a place' : 'No places match "${_query.text}"'),
                        key: const Key('place.empty'),
                        style: AppText.body(14, color: AppColors.muted),
                      ),
                    )
                  : ListView.separated(
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      itemCount: _results.length,
                      separatorBuilder: (context, index) => const Hairline(),
                      itemBuilder: (context, i) {
                        final p = _results[i];
                        return InkWell(
                          key: Key('place.${p.id}'),
                          onTap: () => Navigator.of(context).pop(p),
                          child: Padding(
                            padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 4),
                            child: Row(
                              children: [
                                const AppIcon(AppIcons.pin, size: 20, color: AppColors.muted),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment: CrossAxisAlignment.start,
                                    children: [
                                      Text(p.title, style: AppText.body(15, weight: FontWeight.w600)),
                                      if (p.detail.isNotEmpty)
                                        Text(p.detail, style: AppText.body(13, color: AppColors.muted)),
                                    ],
                                  ),
                                ),
                              ],
                            ),
                          ),
                        );
                      },
                    ),
            ),
          ],
        ),
      ),
    );
  }
}
